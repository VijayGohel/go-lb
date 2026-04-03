package proxy_test

import (
	"encoding/base64"
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
	"github.com/VijayGohel/go-lb/internal/sticky"
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

// --- Sticky session integration tests ---

// newStickyLB wires backends into a pool with round-robin + sticky sessions.
func newStickyLB(backends ...*backend.Backend) *proxy.LoadBalancer {
	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	sa := sticky.New("golb_backend", 1*time.Hour)
	lb := proxy.New(p, rr, proxy.WithStickySession(sa))
	for _, b := range backends {
		lb.SetupProxy(b)
		p.AddBackend(b)
	}
	return lb
}

func TestLb_StickyRouting_CookieRoutesToSameBackend(t *testing.T) {
	var hits [2]int64
	servers := make([]*httptest.Server, 2)
	for i := range servers {
		i := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&hits[i], 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer servers[i].Close()
	}

	lb := newStickyLB(
		makeBackend(servers[0].URL, true),
		makeBackend(servers[1].URL, true),
	)

	// First request: no cookie, algo picks a backend and sets cookie.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rw.Code)
	}

	// Extract the sticky cookie from the response.
	var stickyCookie *http.Cookie
	for _, c := range rw.Result().Cookies() {
		if c.Name == "golb_backend" {
			stickyCookie = c
			break
		}
	}
	if stickyCookie == nil {
		t.Fatal("expected golb_backend cookie in response")
	}

	// Send 10 requests with the sticky cookie -- all should go to the same backend.
	pinnedValue := stickyCookie.Value
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(stickyCookie)
		rw := httptest.NewRecorder()
		lb.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("sticky request %d: expected 200, got %d", i, rw.Code)
		}
		// Verify the response still sets the same cookie value.
		for _, c := range rw.Result().Cookies() {
			if c.Name == "golb_backend" && c.Value != pinnedValue {
				t.Errorf("sticky request %d: cookie changed from %s to %s", i, pinnedValue, c.Value)
			}
		}
	}
}

func TestLb_StickyFallback_DeadBackendFallsToAlgo(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	lb := newStickyLB(
		makeBackend("http://localhost:19996", false), // dead backend
		makeBackend(alive.URL, true),
	)

	// Send a request with a cookie pointing to the dead backend (base64-encoded with StdEncoding).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	encodedDead := base64.StdEncoding.EncodeToString([]byte("http://localhost:19996"))
	req.AddCookie(&http.Cookie{Name: "golb_backend", Value: encodedDead})
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200 (fallback to alive backend), got %d", rw.Code)
	}

	// Verify cookie now points to the alive backend.
	for _, c := range rw.Result().Cookies() {
		if c.Name == "golb_backend" {
			decoded, err := base64.StdEncoding.DecodeString(c.Value)
			if err != nil {
				t.Fatalf("cookie value not valid base64: %v", err)
			}
			if string(decoded) != alive.URL {
				t.Errorf("expected cookie to be updated to alive backend %s, got %s", alive.URL, string(decoded))
			}
			return
		}
	}
	t.Error("expected golb_backend cookie in response")
}

func TestLb_Sticky10Requests_SameBackend(t *testing.T) {
	var hits [3]int64
	servers := make([]*httptest.Server, 3)
	for i := range servers {
		i := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&hits[i], 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer servers[i].Close()
	}

	lb := newStickyLB(
		makeBackend(servers[0].URL, true),
		makeBackend(servers[1].URL, true),
		makeBackend(servers[2].URL, true),
	)

	// First request to establish the sticky cookie.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rw.Code)
	}

	var stickyCookie *http.Cookie
	for _, c := range rw.Result().Cookies() {
		if c.Name == "golb_backend" {
			stickyCookie = c
			break
		}
	}
	if stickyCookie == nil {
		t.Fatal("expected golb_backend cookie in response")
	}

	// Determine which backend was chosen (decode base64 cookie value).
	decodedPinned, err := base64.StdEncoding.DecodeString(stickyCookie.Value)
	if err != nil {
		t.Fatalf("cookie value not valid base64: %v", err)
	}
	pinnedURL := string(decodedPinned)
	pinnedIdx := -1
	for i, srv := range servers {
		if srv.URL == pinnedURL {
			pinnedIdx = i
			break
		}
	}
	if pinnedIdx == -1 {
		t.Fatalf("pinned backend URL %q not found in backends", pinnedURL)
	}

	// Reset hits after the first request.
	for i := range hits {
		atomic.StoreInt64(&hits[i], 0)
	}

	// Send 10 more requests with the sticky cookie.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(stickyCookie)
		rw := httptest.NewRecorder()
		lb.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rw.Code)
		}
	}

	// All 10 requests should have gone to the pinned backend.
	if atomic.LoadInt64(&hits[pinnedIdx]) != 10 {
		t.Errorf("pinned backend (idx %d) got %d requests, want 10", pinnedIdx, hits[pinnedIdx])
	}
	// Other backends should have 0.
	for i, h := range hits {
		if i != pinnedIdx && atomic.LoadInt64(&h) != 0 {
			t.Errorf("non-pinned backend (idx %d) got %d requests, want 0", i, h)
		}
	}
}
