package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLb_ProxiesToAliveBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	pool := &ServerPool{}
	b := makeBackend(backend.URL, true)
	setupProxy(b, pool)
	pool.AddBackend(b)
	serverPool = *pool

	initLogger()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
}

func TestLb_Returns503_WhenNoBackendsAlive(t *testing.T) {
	pool := &ServerPool{}
	pool.AddBackend(makeBackend("http://localhost:19998", false))
	serverPool = *pool

	initLogger()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb(rw, req)

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

	pool := &ServerPool{}
	for _, srv := range backends {
		b := makeBackend(srv.URL, true)
		setupProxy(b, pool)
		pool.AddBackend(b)
	}
	serverPool = *pool
	initLogger()

	for i := 0; i < 9; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rw := httptest.NewRecorder()
		lb(rw, req)
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
	// Reserve a port and close it immediately to guarantee connection refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := "http://" + ln.Addr().String()
	ln.Close()

	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	pool := &ServerPool{}
	dead := makeBackend(deadAddr, true)
	setupProxy(dead, pool)
	pool.AddBackend(dead)

	b := makeBackend(alive.URL, true)
	setupProxy(b, pool)
	pool.AddBackend(b)

	serverPool = *pool
	initLogger()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200 after switching to alive backend, got %d", rw.Code)
	}
}

func TestLb_AllBackendsFail_Returns503(t *testing.T) {
	var addrs []string
	for i := 0; i < maxBackendSwitches; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, "http://"+ln.Addr().String())
		ln.Close()
	}

	pool := &ServerPool{}
	for _, addr := range addrs {
		b := makeBackend(addr, true)
		setupProxy(b, pool)
		pool.AddBackend(b)
	}
	serverPool = *pool
	initLogger()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when all backends fail, got %d", rw.Code)
	}
}

func TestResponseWriter_CapturesStatusCode(t *testing.T) {
	inner := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: inner, statusCode: http.StatusOK}
	rw.WriteHeader(http.StatusCreated)

	if rw.statusCode != http.StatusCreated {
		t.Errorf("statusCode = %d, want 201", rw.statusCode)
	}
}
