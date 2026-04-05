package proxy_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
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

// --- Sticky + Circuit Breaker integration ---

func TestLb_Sticky_CircuitOpen_FallsBackToAlgo(t *testing.T) {
	var aliveHits int64
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&aliveHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer dead.Close()

	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	cbReg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          10 * time.Second,
	})
	sa := sticky.New("golb_backend", 1*time.Hour)

	lb := proxy.New(p, rr,
		proxy.WithCircuitBreaker(cbReg),
		proxy.WithStickySession(sa),
	)

	bDead := &backend.Backend{URL: mustParseURL(dead.URL), Weight: 1}
	bDead.SetAlive(true)
	lb.SetupProxy(bDead)
	p.AddBackend(bDead)

	bAlive := &backend.Backend{URL: mustParseURL(alive.URL), Weight: 1}
	bAlive.SetAlive(true)
	lb.SetupProxy(bAlive)
	p.AddBackend(bAlive)

	// Trip circuit breaker on the "dead" backend.
	cbReg.Get(dead.URL).RecordFailure()
	if cbReg.Get(dead.URL).State() != circuitbreaker.Open {
		t.Fatal("expected dead backend circuit to be Open")
	}

	// Send request with sticky cookie pointing to the circuit-open backend.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(dead.URL))
	req.AddCookie(&http.Cookie{Name: "golb_backend", Value: encoded})
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200 (fallback to alive), got %d", rw.Code)
	}
	if got := atomic.LoadInt64(&aliveHits); got != 1 {
		t.Errorf("expected alive backend to serve request, got %d hits", got)
	}
}

// --- Concurrent reload + requests ---

func TestLb_ConcurrentReloadAndRequests(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	lb := proxy.New(p, rr)

	b := &backend.Backend{URL: mustParseURL(srv.URL), Weight: 1}
	b.SetAlive(true)
	lb.SetupProxy(b)
	p.AddBackend(b)

	var wg sync.WaitGroup

	// Send requests concurrently.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rw := httptest.NewRecorder()
			lb.ServeHTTP(rw, req)
		}()
	}

	// Concurrently add and remove backends.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, _ := url.Parse("http://localhost:29999")
			extra := &backend.Backend{URL: u, Weight: 1}
			extra.SetAlive(false)
			lb.SetupProxy(extra)
			p.AddBackend(extra)
			time.Sleep(time.Millisecond)
			p.Remove("http://localhost:29999")
		}()
	}

	// Concurrently swap algorithm.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lc, _ := algo.New("least_connections")
			lb.SetAlgorithm(lc)
			time.Sleep(time.Millisecond)
			newRR, _ := algo.New("round_robin")
			lb.SetAlgorithm(newRR)
		}()
	}

	wg.Wait()
	// No panics, deadlocks, or races = pass.
	if atomic.LoadInt64(&hits) == 0 {
		t.Error("expected at least some requests to succeed")
	}
}
