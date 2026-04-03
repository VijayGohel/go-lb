package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	Closed   State = iota // normal operation, counting failures
	Open                  // rejecting requests, waiting for timeout
	HalfOpen              // allowing one probe request
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config holds the circuit breaker parameters.
type Config struct {
	FailureThreshold int
	SuccessThreshold int
	Timeout          time.Duration
}

// Breaker is a per-backend circuit breaker.
type Breaker struct {
	mu               sync.Mutex
	state            State
	failures         int
	successes        int
	failureThreshold int
	successThreshold int
	timeout          time.Duration
	lastFailure      time.Time
	halfOpenPermit   atomic.Bool
}

// NewBreaker creates a Breaker with the given thresholds and timeout.
func NewBreaker(failureThreshold, successThreshold int, timeout time.Duration) *Breaker {
	if failureThreshold < 1 {
		failureThreshold = 1
	}
	if successThreshold < 1 {
		successThreshold = 1
	}
	if timeout < time.Millisecond {
		timeout = time.Millisecond
	}
	return &Breaker{
		state:            Closed,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
	}
}

// Allow reports whether a request should be allowed through.
// In Closed state, always returns true.
// In Open state, returns false unless the timeout has elapsed (transitions to HalfOpen).
// In HalfOpen state, allows exactly one probe request.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Closed:
		return true
	case Open:
		if time.Since(b.lastFailure) >= b.timeout {
			b.state = HalfOpen
			b.halfOpenPermit.Store(true)
			return b.halfOpenPermit.CompareAndSwap(true, false)
		}
		return false
	case HalfOpen:
		return b.halfOpenPermit.CompareAndSwap(true, false)
	default:
		return true
	}
}

// RecordSuccess records a successful request.
// In HalfOpen state, after successThreshold successes, transitions to Closed.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case HalfOpen:
		b.successes++
		if b.successes >= b.successThreshold {
			b.state = Closed
			b.failures = 0
			b.successes = 0
		}
		// Allow next probe request
		b.halfOpenPermit.Store(true)
	case Closed:
		b.failures = 0
	}
}

// RecordFailure records a failed request.
// In Closed state, after failureThreshold consecutive failures, transitions to Open.
// In HalfOpen state, transitions back to Open.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Closed:
		b.failures++
		if b.failures >= b.failureThreshold {
			b.state = Open
			b.lastFailure = time.Now()
			b.failures = 0
			b.successes = 0
		}
	case HalfOpen:
		b.state = Open
		b.lastFailure = time.Now()
		b.failures = 0
		b.successes = 0
	}
}

// State returns the current circuit state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Eligible reports whether the breaker would allow a request without
// consuming state. Use this for filtering backends before selection.
// Closed and HalfOpen are eligible; Open is eligible only if timeout has elapsed.
func (b *Breaker) Eligible() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		return true
	case Open:
		return time.Since(b.lastFailure) >= b.timeout
	case HalfOpen:
		return true
	default:
		return true
	}
}

// Registry holds circuit breakers keyed by backend URL.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	cfg      Config
}

// NewRegistry creates a Registry that lazy-creates breakers with the given config.
func NewRegistry(cfg Config) *Registry {
	return &Registry{
		breakers: make(map[string]*Breaker),
		cfg:      cfg,
	}
}

// Get returns the breaker for the given backend URL, creating one if absent.
func (r *Registry) Get(backendURL string) *Breaker {
	r.mu.RLock()
	cb, ok := r.breakers[backendURL]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if cb, ok = r.breakers[backendURL]; ok {
		return cb
	}
	cb = NewBreaker(r.cfg.FailureThreshold, r.cfg.SuccessThreshold, r.cfg.Timeout)
	r.breakers[backendURL] = cb
	return cb
}

// Remove deletes the breaker for the given backend URL.
func (r *Registry) Remove(backendURL string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.breakers, backendURL)
}
