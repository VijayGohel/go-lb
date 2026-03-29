package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/pool"
)

const (
	// MaxBackendSwitches is the maximum number of backend switches allowed per request.
	MaxBackendSwitches = 3
	maxRetries         = 3 // per-backend retries before marking dead and switching
)

// idempotentMethods are safe to retry — repeating them has no observable side effects.
var idempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// contextKey is a private type for context values to avoid collisions.
type contextKey int

const (
	attemptsKey  contextKey = iota // number of backend switches so far (0-based)
	retryKey                       // number of retries on the current backend
	requestIDKey                   // stable ID propagated across backend switches
)

// LoadBalancer routes requests across a pool of backends using round-robin with retry.
// It implements http.Handler.
type LoadBalancer struct {
	pool *pool.ServerPool
}

// New creates a LoadBalancer backed by the given pool.
func New(p *pool.ServerPool) *LoadBalancer {
	return &LoadBalancer{pool: p}
}

func getAttemptsFromContext(r *http.Request) int {
	if v, ok := r.Context().Value(attemptsKey).(int); ok {
		return v
	}
	return 0
}

func getRetryFromContext(r *http.Request) int {
	if v, ok := r.Context().Value(retryKey).(int); ok {
		return v
	}
	return 0
}

// getOrCreateRequestID returns the stable request ID from context, creating one if absent.
// Falls back to a timestamp-based ID if crypto/rand fails.
func getOrCreateRequestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey).(string); ok {
		return id
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		slog.Warn("rand_read_failed", "error", err.Error())
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// SetupProxy attaches a retry-aware error handler to the backend's ReverseProxy.
// Call this for every backend before adding it to the pool.
func (lb *LoadBalancer) SetupProxy(b *backend.Backend) {
	proxy := httputil.NewSingleHostReverseProxy(b.URL)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		retry := getRetryFromContext(r)
		if retry < maxRetries && idempotentMethods[r.Method] {
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-timer.C:
				ctx := context.WithValue(r.Context(), retryKey, retry+1)
				proxy.ServeHTTP(w, r.WithContext(ctx))
			case <-r.Context().Done():
				timer.Stop()
				return
			}
			return
		}
		lb.pool.MarkBackendStatus(b.URL, false)
		slog.Warn("backend_down", "backend", b.URL.String(), "error", e.Error())

		attempts := getAttemptsFromContext(r)
		if attempts < MaxBackendSwitches {
			ctx := context.WithValue(r.Context(), attemptsKey, attempts+1)
			ctx = context.WithValue(ctx, retryKey, 0)
			lb.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
	}
	b.ReverseProxy = proxy
}

// ServeHTTP picks the next alive backend and forwards the request.
// It implements http.Handler.
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	attempts := getAttemptsFromContext(r)
	if attempts >= MaxBackendSwitches {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}
	peer := lb.pool.GetNextPeer()
	if peer == nil {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	requestID := getOrCreateRequestID(r)
	if _, ok := r.Context().Value(requestIDKey).(string); !ok {
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID))
	}

	start := time.Now()
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	peer.ReverseProxy.ServeHTTP(rw, r)
	slog.Info("request",
		"request_id", requestID,
		"backend", peer.URL.String(),
		"latency_ms", time.Since(start).Milliseconds(),
		"status", rw.statusCode,
		"attempt", attempts+1,
	)
}

// responseWriter wraps http.ResponseWriter to capture the status code for logging.
// It forwards optional interfaces (Flusher, Hijacker) so streaming and WebSocket
// upgrades continue to work through the reverse proxy.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}
