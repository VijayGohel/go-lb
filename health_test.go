package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthChecker_MarksBackendAlive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	pool := &ServerPool{}
	b := makeBackend(srv.URL, false)
	pool.AddBackend(b)

	initLogger()
	hc := NewHealthChecker(pool, "/health", time.Second, time.Second)
	hc.checkBackend(b)

	if !b.IsAlive() {
		t.Fatal("backend should be marked alive after 200 health check")
	}
}

func TestHealthChecker_MarksBackendDead_On500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	pool := &ServerPool{}
	b := makeBackend(srv.URL, true)
	pool.AddBackend(b)

	initLogger()
	hc := NewHealthChecker(pool, "/health", time.Second, time.Second)
	hc.checkBackend(b)

	if b.IsAlive() {
		t.Fatal("backend should be dead after 500 health response")
	}
}

func TestHealthChecker_MarksBackendDead_OnConnectionRefused(t *testing.T) {
	pool := &ServerPool{}
	b := makeBackend("http://localhost:19999", true)
	pool.AddBackend(b)

	initLogger()
	hc := NewHealthChecker(pool, "/health", time.Second, 200*time.Millisecond)
	hc.checkBackend(b)

	if b.IsAlive() {
		t.Fatal("backend should be dead when connection refused")
	}
}
