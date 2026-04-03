package metrics

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	initialized atomic.Bool
	enabled     atomic.Bool

	requestsTotal     *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	backendUp         *prometheus.GaugeVec
	activeConnections *prometheus.GaugeVec
)

// safeRegisterCounterVec registers the collector or, if an identical one is
// already registered, extracts and returns the existing instance.
// This avoids panics from MustRegister and is safe for repeated Init calls
// and test suites that share a global registry.
func safeRegisterCounterVec(c *prometheus.CounterVec) *prometheus.CounterVec {
	if err := prometheus.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			return are.ExistingCollector.(*prometheus.CounterVec)
		}
		panic(err) // unexpected registration error
	}
	return c
}

// safeRegisterHistogramVec registers the collector or reuses an existing one.
func safeRegisterHistogramVec(c *prometheus.HistogramVec) *prometheus.HistogramVec {
	if err := prometheus.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			return are.ExistingCollector.(*prometheus.HistogramVec)
		}
		panic(err)
	}
	return c
}

// safeRegisterGaugeVec registers the collector or reuses an existing one.
func safeRegisterGaugeVec(c *prometheus.GaugeVec) *prometheus.GaugeVec {
	if err := prometheus.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			return are.ExistingCollector.(*prometheus.GaugeVec)
		}
		panic(err)
	}
	return c
}

// Init registers Prometheus collectors and enables metric recording.
// It is safe to call multiple times; only the first call has an effect.
func Init() {
	if !initialized.CompareAndSwap(false, true) {
		return
	}

	requestsTotal = safeRegisterCounterVec(prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "golb_requests_total",
			Help: "Total number of requests proxied, labelled by backend and HTTP status code.",
		},
		[]string{"backend", "code"},
	))

	requestDuration = safeRegisterHistogramVec(prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "golb_request_duration_seconds",
			Help:    "Histogram of proxy request latencies in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"backend"},
	))

	backendUp = safeRegisterGaugeVec(prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "golb_backend_up",
			Help: "Whether a backend is considered alive (1) or dead (0).",
		},
		[]string{"backend"},
	))

	activeConnections = safeRegisterGaugeVec(prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "golb_active_connections",
			Help: "Number of in-flight connections per backend.",
		},
		[]string{"backend"},
	))

	enabled.Store(true)
}

// RecordRequest records a completed proxied request.
// It is a no-op when metrics are not enabled.
func RecordRequest(backend string, statusCode int, duration time.Duration) {
	if !enabled.Load() {
		return
	}
	code := statusCodeLabel(statusCode)
	requestsTotal.WithLabelValues(backend, code).Inc()
	requestDuration.WithLabelValues(backend).Observe(duration.Seconds())
}

// SetBackendUp sets the backend_up gauge to 1 (alive) or 0 (dead).
// It is a no-op when metrics are not enabled.
func SetBackendUp(backend string, up bool) {
	if !enabled.Load() {
		return
	}
	v := 0.0
	if up {
		v = 1.0
	}
	backendUp.WithLabelValues(backend).Set(v)
}

// IncrActiveConns increments the active connections gauge for a backend.
// It is a no-op when metrics are not enabled.
func IncrActiveConns(backend string) {
	if !enabled.Load() {
		return
	}
	activeConnections.WithLabelValues(backend).Inc()
}

// DecrActiveConns decrements the active connections gauge for a backend.
// It is a no-op when metrics are not enabled.
func DecrActiveConns(backend string) {
	if !enabled.Load() {
		return
	}
	activeConnections.WithLabelValues(backend).Dec()
}

// statusCodeLabel returns a short bucket string for the HTTP status code.
func statusCodeLabel(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "other"
	}
}
