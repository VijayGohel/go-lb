package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"
)

const (
	maxRetries         = 3 // per-backend retries before marking it dead and switching
	maxBackendSwitches = 3 // total backend switches allowed per request
)

// idempotentMethods are safe to retry — their bodies are not consumed on the first attempt
// in a way that would corrupt a retry, and repeating them has no side effects.
var idempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// Context keys for per-request retry tracking.
type contextKey int

const (
	attemptsKey  contextKey = iota // number of backend switches so far (0-based)
	retryKey                       // number of retries on the current backend
	requestIDKey                   // stable ID propagated across backend switches
)

// serverPool is the package-level pool used by the lb handler.
// Initialised in main(). Proxy tests set it directly.
var serverPool ServerPool

// getAttemptsFromContext returns how many backend switches have occurred (0 = first backend).
func getAttemptsFromContext(r *http.Request) int {
	if v, ok := r.Context().Value(attemptsKey).(int); ok {
		return v
	}
	return 0
}

// getRetryFromContext returns how many retries on the current backend have happened.
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
		logger.Warn("rand_read_failed", "error", err.Error())
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// setupProxy attaches an error handler to the backend's ReverseProxy.
// Call this for every backend after creating it.
func setupProxy(b *Backend, pool *ServerPool) {
	proxy := httputil.NewSingleHostReverseProxy(b.URL)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		retry := getRetryFromContext(r)
		if retry < maxRetries && idempotentMethods[r.Method] {
			select {
			case <-time.After(10 * time.Millisecond):
				ctx := context.WithValue(r.Context(), retryKey, retry+1)
				proxy.ServeHTTP(w, r.WithContext(ctx))
			case <-r.Context().Done():
				return
			}
			return
		}
		pool.MarkBackendStatus(b.URL, false)
		logger.Warn("backend_down", "backend", b.URL.String(), "error", e.Error())

		attempts := getAttemptsFromContext(r)
		if attempts < maxBackendSwitches {
			// Reset retryKey so the next backend gets its own retry budget.
			ctx := context.WithValue(r.Context(), attemptsKey, attempts+1)
			ctx = context.WithValue(ctx, retryKey, 0)
			lb(w, r.WithContext(ctx))
			return
		}
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
	}
	b.ReverseProxy = proxy
}

// lb is the main load balancer HTTP handler.
func lb(w http.ResponseWriter, r *http.Request) {
	attempts := getAttemptsFromContext(r)
	if attempts >= maxBackendSwitches {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}
	peer := serverPool.GetNextPeer()
	if peer == nil {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	// Propagate a stable request_id across backend switches so all log entries
	// for the same original request share the same ID.
	requestID := getOrCreateRequestID(r)
	if _, ok := r.Context().Value(requestIDKey).(string); !ok {
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID))
	}

	start := time.Now()
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	peer.ReverseProxy.ServeHTTP(rw, r)
	logger.Info("request",
		"request_id", requestID,
		"backend", peer.URL.String(),
		"latency_ms", time.Since(start).Milliseconds(),
		"status", rw.statusCode,
		"attempt", attempts+1,
	)
}

// responseWriter wraps http.ResponseWriter to capture the status code for logging.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
