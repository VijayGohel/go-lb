package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"
)

// Context keys for per-request retry tracking.
type contextKey int

const (
	attemptsKey contextKey = iota
	retryKey
)

// serverPool is the package-level pool used by the lb handler.
// Initialised in main(). Proxy tests set it directly.
var serverPool ServerPool

// getAttemptsFromContext returns how many full backend switches have happened.
func getAttemptsFromContext(r *http.Request) int {
	if v, ok := r.Context().Value(attemptsKey).(int); ok {
		return v
	}
	return 1
}

// getRetryFromContext returns how many retries on the current backend have happened.
func getRetryFromContext(r *http.Request) int {
	if v, ok := r.Context().Value(retryKey).(int); ok {
		return v
	}
	return 0
}

// setupProxy attaches an error handler to the backend's ReverseProxy.
// Call this for every backend after creating it.
func setupProxy(b *Backend, pool *ServerPool) {
	proxy := httputil.NewSingleHostReverseProxy(b.URL)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		retry := getRetryFromContext(r)
		if retry < 3 {
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
		if attempts < 3 {
			// Reset retryKey so the next backend gets its own 3 retries.
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
	if getAttemptsFromContext(r) > 3 {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}
	peer := serverPool.GetNextPeer()
	if peer == nil {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	var b [8]byte
	rand.Read(b[:])
	requestID := fmt.Sprintf("%x", b)

	start := time.Now()
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	peer.ReverseProxy.ServeHTTP(rw, r)
	logger.Info("request",
		"request_id", requestID,
		"backend", peer.URL.String(),
		"latency_ms", time.Since(start).Milliseconds(),
		"status", rw.statusCode,
		"attempt", getAttemptsFromContext(r),
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
