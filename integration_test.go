package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegration_RoundRobinDistribution(t *testing.T) {
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

func TestIntegration_DeadBackendSkipped(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	pool := &ServerPool{}
	dead := makeBackend("http://localhost:19997", false)
	setupProxy(dead, pool)
	pool.AddBackend(dead)

	b := makeBackend(alive.URL, true)
	setupProxy(b, pool)
	pool.AddBackend(b)

	serverPool = *pool
	initLogger()

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rw := httptest.NewRecorder()
		lb(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d — dead backend not skipped", i, rw.Code)
		}
	}
}

func TestIntegration_HealthChecker_RecoverDeadBackend(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	pool := &ServerPool{}
	b := makeBackend(srv.URL, false)
	pool.AddBackend(b)

	initLogger()
	hc := NewHealthChecker(pool, "/health", 100*time.Millisecond, time.Second)

	hc.checkBackend(b)
	if b.IsAlive() {
		t.Fatal("backend should be dead when returning 500")
	}

	healthy.Store(true)
	hc.checkBackend(b)

	if !b.IsAlive() {
		t.Fatal("backend should recover to alive after 200 health check")
	}
	fmt.Println("backend recovered successfully")
}
