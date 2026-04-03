package ratelimit

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VijayGohel/go-lb/internal/middleware"
)

// bucket implements a self-contained token bucket rate limiter.
type bucket struct {
	mu       sync.Mutex
	tokens   float64
	max      float64   // burst size
	rate     float64   // tokens per second
	lastTime time.Time // last refill timestamp
}

func newBucket(rate float64, burst int) *bucket {
	return &bucket{
		tokens:   float64(burst),
		max:      float64(burst),
		rate:     rate,
		lastTime: time.Now(),
	}
}

// allow returns true if a token is available and consumes it.
func (b *bucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.max {
		b.tokens = b.max
	}
	b.lastTime = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// retryAfterSeconds returns the estimated seconds until a token is available.
func (b *bucket) retryAfterSeconds() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.rate <= 0 {
		return 1
	}
	deficit := 1.0 - b.tokens
	if deficit <= 0 {
		return 1
	}
	secs := deficit / b.rate
	return int(math.Ceil(secs))
}

// entry holds a per-IP bucket and a last-seen timestamp for cleanup.
type entry struct {
	bucket   *bucket
	lastSeen atomic.Int64 // unix timestamp
}

// Limiter provides token bucket rate limiting in global or per-IP mode.
type Limiter struct {
	global   *bucket  // used when perIPOn=false
	perIP    sync.Map // string -> *entry; used when perIPOn=true
	rate     float64
	burst    int
	perIPOn  bool
	done     chan struct{}
	stopOnce sync.Once
}

// New creates a Limiter. When perIP is true, each unique IP gets its own
// token bucket; otherwise a single global bucket is shared.
// It returns an error if rps <= 0 or burst < 1.
func New(rps float64, burst int, perIP bool) (*Limiter, error) {
	if rps <= 0 {
		return nil, fmt.Errorf("ratelimit: requests_per_second must be > 0, got %g", rps)
	}
	if burst < 1 {
		return nil, fmt.Errorf("ratelimit: burst must be >= 1, got %d", burst)
	}
	l := &Limiter{
		rate:    rps,
		burst:   burst,
		perIPOn: perIP,
		done:    make(chan struct{}),
	}
	if !perIP {
		l.global = newBucket(rps, burst)
	} else {
		go l.cleanup()
	}
	return l, nil
}

// Allow checks whether a request from ip is permitted.
func (l *Limiter) Allow(ip string) bool {
	if !l.perIPOn {
		return l.global.allow()
	}

	now := time.Now().Unix()

	val, loaded := l.perIP.Load(ip)
	if !loaded {
		e := &entry{bucket: newBucket(l.rate, l.burst)}
		e.lastSeen.Store(now)
		actual, _ := l.perIP.LoadOrStore(ip, e)
		val = actual
	}

	e := val.(*entry)
	e.lastSeen.Store(now)
	return e.bucket.allow()
}

// Middleware returns a middleware.Middleware that enforces the rate limit.
// Rejected requests receive 429 Too Many Requests with a dynamic Retry-After header
// based on the token bucket's estimated refill time.
func (l *Limiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ip string
			if l.perIPOn {
				ip = clientIP(r)
			}
			if !l.Allow(ip) {
				retryAfter := l.retryAfter(ip)
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// retryAfter returns the estimated seconds until a token is available for ip.
func (l *Limiter) retryAfter(ip string) int {
	if !l.perIPOn {
		return l.global.retryAfterSeconds()
	}
	val, ok := l.perIP.Load(ip)
	if !ok {
		return 1
	}
	return val.(*entry).bucket.retryAfterSeconds()
}

// Stop halts the background cleanup goroutine. Safe to call multiple times.
func (l *Limiter) Stop() {
	l.stopOnce.Do(func() { close(l.done) })
}

// cleanup runs every 60 seconds and evicts per-IP entries not seen
// in the last 5 minutes.
func (l *Limiter) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-5 * time.Minute).Unix()
			l.perIP.Range(func(key, value any) bool {
				e := value.(*entry)
				if e.lastSeen.Load() < cutoff {
					l.perIP.Delete(key)
				}
				return true
			})
		}
	}
}

// clientIP extracts the client IP from r.RemoteAddr, stripping the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
