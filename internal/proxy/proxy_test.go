package proxy_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
)

func mustParseURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

func makeBackend(rawURL string, alive bool) *backend.Backend {
	b := &backend.Backend{URL: mustParseURL(rawURL)}
	b.SetAlive(alive)
	return b
}

// newLB wires backends into a pool and returns a ready LoadBalancer.
func newLB(backends ...*backend.Backend) *proxy.LoadBalancer {
	p := &pool.ServerPool{}
	lb := proxy.New(p)
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
	lb := proxy.New(p)
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
	ln.Close()

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
		ln.Close()
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
