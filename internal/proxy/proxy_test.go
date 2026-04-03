package proxy_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/circuitbreaker"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
)

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func makeBackend(rawURL string, alive bool) *backend.Backend {
	b := &backend.Backend{URL: mustParseURL(rawURL), Weight: 1}
	b.SetAlive(alive)
	return b
}

// newLB wires backends into a pool with round-robin and returns a ready LoadBalancer.
func newLB(backends ...*backend.Backend) *proxy.LoadBalancer {
	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	lb := proxy.New(p, rr)
	for _, b := range backends {
		lb.SetupProxy(b)
		p.AddBackend(b)
	}
	return lb
}

func TestLb_ProxiesToAliveBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	lb := newLB(makeBackend(srv.URL, true))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
}

func TestLb_Returns503_WhenNoBackendsAlive(t *testing.T) {
	lb := newLB(makeBackend("http://localhost:19998", false))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rw.Code)
	}
}

func TestLb_RoundRobin(t *testing.T) {
	var hits [3]int64
	backends := make([]*httptest.Server, 3)
	for i := range backends {
		i := i
		backends[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&hits[i], 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer backends[i].Close()
	}

	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	lb := proxy.New(p, rr)
	for _, srv := range backends {
		b := makeBackend(srv.URL, true)
		lb.SetupProxy(b)
		p.AddBackend(b)
	}

	for i := 0; i < 9; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rw := httptest.NewRecorder()
		lb.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rw.Code)
		}
	}
	for i, h := range hits {
		if h != 3 {
			t.Errorf("backend %d got %d requests, want 3", i, h)
		}
	}
}

func TestLb_SwitchesBackend_OnFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	lb := newLB(makeBackend(deadAddr, true), makeBackend(alive.URL, true))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200 after switching to alive backend, got %d", rw.Code)
	}
}

func TestLb_AllBackendsFail_Returns503(t *testing.T) {
	var deadBackends []*backend.Backend
	for i := 0; i < proxy.MaxBackendSwitches; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		deadBackends = append(deadBackends, makeBackend("http://"+ln.Addr().String(), true))
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
	}

	lb := newLB(deadBackends...)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when all backends fail, got %d", rw.Code)
	}
}

func TestLb_DeadBackendSkipped(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	lb := newLB(
		makeBackend("http://localhost:19997", false),
		makeBackend(alive.URL, true),
	)

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rw := httptest.NewRecorder()
		lb.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d — dead backend not skipped", i, rw.Code)
		}
	}
}

func TestLb_CircuitBreaker_OpenBackendSkipped(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")

	cbReg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          10 * time.Second,
	})
	lb := proxy.New(p, rr, proxy.WithCircuitBreaker(cbReg))

	b1 := makeBackend("http://localhost:19990", true) // will be circuit-opened
	b2 := makeBackend(alive.URL, true)
	lb.SetupProxy(b1)
	lb.SetupProxy(b2)
	p.AddBackend(b1)
	p.AddBackend(b2)

	// Trip circuit on b1
	cbReg.Get("http://localhost:19990").RecordFailure()
	if cbReg.Get("http://localhost:19990").State() != circuitbreaker.Open {
		t.Fatal("expected b1 circuit to be Open")
	}

	// All requests should go to b2 (b1 is circuit-open)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rw := httptest.NewRecorder()
		lb.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rw.Code)
		}
	}
}

func TestLb_CircuitBreaker_SuccessClosesCircuit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")

	cbReg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          10 * time.Millisecond,
	})
	lb := proxy.New(p, rr, proxy.WithCircuitBreaker(cbReg))

	b := makeBackend(srv.URL, true)
	lb.SetupProxy(b)
	p.AddBackend(b)

	// Trip circuit
	cbReg.Get(srv.URL).RecordFailure()
	cbReg.Get(srv.URL).RecordFailure()
	if cbReg.Get(srv.URL).State() != circuitbreaker.Open {
		t.Fatal("expected Open")
	}

	// Wait for timeout to allow half-open
	time.Sleep(20 * time.Millisecond)

	// Successful request should close the circuit
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
	if cbReg.Get(srv.URL).State() != circuitbreaker.Closed {
		t.Errorf("expected Closed after successful probe, got %s", cbReg.Get(srv.URL).State())
	}
}
