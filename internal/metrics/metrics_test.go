package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// resetForTest tears down global state so each test starts clean.
func resetForTest() {
	initialized.Store(false)
	enabled.Store(false)
	// Unregister collectors if they were registered by a previous test.
	if requestsTotal != nil {
		prometheus.Unregister(requestsTotal)
	}
	if requestDuration != nil {
		prometheus.Unregister(requestDuration)
	}
	if backendUp != nil {
		prometheus.Unregister(backendUp)
	}
	if activeConnections != nil {
		prometheus.Unregister(activeConnections)
	}
	requestsTotal = nil
	requestDuration = nil
	backendUp = nil
	activeConnections = nil
}

func TestNoOpBeforeInit(t *testing.T) {
	resetForTest()
	// These must not panic even though collectors are nil.
	RecordRequest("http://localhost:8080", 200, 50*time.Millisecond)
	SetBackendUp("http://localhost:8080", true)
	IncrActiveConns("http://localhost:8080")
	DecrActiveConns("http://localhost:8080")
}

func TestInitIdempotent(t *testing.T) {
	resetForTest()
	Init()
	Init() // second call must be a no-op
	if !initialized.Load() {
		t.Fatal("expected initialized to be true")
	}
	if !enabled.Load() {
		t.Fatal("expected enabled to be true")
	}
}

func TestRecordRequest(t *testing.T) {
	resetForTest()
	Init()

	RecordRequest("http://localhost:8080", 200, 100*time.Millisecond)
	RecordRequest("http://localhost:8080", 200, 200*time.Millisecond)
	RecordRequest("http://localhost:8080", 500, 50*time.Millisecond)

	// Verify counter values.
	m := &dto.Metric{}
	if err := requestsTotal.WithLabelValues("http://localhost:8080", "2xx").(prometheus.Metric).Write(m); err != nil {
		t.Fatal(err)
	}
	if got := m.GetCounter().GetValue(); got != 2 {
		t.Errorf("requests_total 2xx: want 2, got %v", got)
	}

	m = &dto.Metric{}
	if err := requestsTotal.WithLabelValues("http://localhost:8080", "5xx").(prometheus.Metric).Write(m); err != nil {
		t.Fatal(err)
	}
	if got := m.GetCounter().GetValue(); got != 1 {
		t.Errorf("requests_total 5xx: want 1, got %v", got)
	}

	// Verify histogram has 3 observations.
	m = &dto.Metric{}
	if err := requestDuration.WithLabelValues("http://localhost:8080").(prometheus.Metric).Write(m); err != nil {
		t.Fatal(err)
	}
	if got := m.GetHistogram().GetSampleCount(); got != 3 {
		t.Errorf("request_duration count: want 3, got %v", got)
	}
}

func TestSetBackendUp(t *testing.T) {
	resetForTest()
	Init()

	SetBackendUp("http://localhost:8080", true)
	m := &dto.Metric{}
	if err := backendUp.WithLabelValues("http://localhost:8080").(prometheus.Metric).Write(m); err != nil {
		t.Fatal(err)
	}
	if got := m.GetGauge().GetValue(); got != 1 {
		t.Errorf("backend_up: want 1, got %v", got)
	}

	SetBackendUp("http://localhost:8080", false)
	m = &dto.Metric{}
	if err := backendUp.WithLabelValues("http://localhost:8080").(prometheus.Metric).Write(m); err != nil {
		t.Fatal(err)
	}
	if got := m.GetGauge().GetValue(); got != 0 {
		t.Errorf("backend_up: want 0, got %v", got)
	}
}

func TestActiveConnections(t *testing.T) {
	resetForTest()
	Init()

	IncrActiveConns("http://localhost:8080")
	IncrActiveConns("http://localhost:8080")
	DecrActiveConns("http://localhost:8080")

	m := &dto.Metric{}
	if err := activeConnections.WithLabelValues("http://localhost:8080").(prometheus.Metric).Write(m); err != nil {
		t.Fatal(err)
	}
	if got := m.GetGauge().GetValue(); got != 1 {
		t.Errorf("active_connections: want 1, got %v", got)
	}
}

func TestStatusCodeLabel(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{200, "2xx"},
		{201, "2xx"},
		{301, "3xx"},
		{404, "4xx"},
		{500, "5xx"},
		{100, "other"},
	}
	for _, tt := range tests {
		if got := statusCodeLabel(tt.code); got != tt.want {
			t.Errorf("statusCodeLabel(%d): want %q, got %q", tt.code, tt.want, got)
		}
	}
}
