package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// --- bucket tests ---

func TestBucket_ConsumeAllTokens_ThenReject(t *testing.T) {
	b := newBucket(10, 5) // 10 rps, burst 5

	// Consume all 5 burst tokens.
	for i := 0; i < 5; i++ {
		if !b.allow() {
			t.Fatalf("allow() should succeed on token %d of 5", i+1)
		}
	}
	// Next request must be rejected.
	if b.allow() {
		t.Fatal("allow() should fail after burst exhaustion")
	}
}

func TestBucket_RefillOverTime(t *testing.T) {
	b := newBucket(100, 1) // 100 rps, burst 1

	// Consume the single token.
	if !b.allow() {
		t.Fatal("first allow() should succeed")
	}
	if b.allow() {
		t.Fatal("second allow() should fail immediately")
	}

	// Advance time by manipulating lastTime directly.
	b.mu.Lock()
	b.lastTime = b.lastTime.Add(-50 * time.Millisecond) // 0.05s * 100 rps = 5 tokens, capped at 1
	b.mu.Unlock()

	if !b.allow() {
		t.Fatal("allow() should succeed after time passes")
	}
}

// --- global limiter tests ---

func TestGlobalLimiter_BurstExhaustion_Returns429(t *testing.T) {
	lim := New(10, 3, false) // global mode, burst 3
	defer lim.Stop()

	mw := lim.Middleware()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	// First 3 requests should succeed.
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i+1, rec.Code)
		}
	}

	// 4th request should be rate-limited.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("want 429, got %d", rec.Code)
	}
}

// --- per-IP limiter tests ---

func TestPerIPLimiter_IndependentLimits(t *testing.T) {
	lim := New(10, 2, true) // per-IP mode, burst 2
	defer lim.Stop()

	// IP-A: exhaust its 2 tokens.
	if !lim.Allow("10.0.0.1") {
		t.Fatal("IP-A token 1 should be allowed")
	}
	if !lim.Allow("10.0.0.1") {
		t.Fatal("IP-A token 2 should be allowed")
	}
	if lim.Allow("10.0.0.1") {
		t.Fatal("IP-A should be rate-limited after burst")
	}

	// IP-B should still have its own tokens.
	if !lim.Allow("10.0.0.2") {
		t.Fatal("IP-B token 1 should be allowed (independent)")
	}
	if !lim.Allow("10.0.0.2") {
		t.Fatal("IP-B token 2 should be allowed (independent)")
	}
	if lim.Allow("10.0.0.2") {
		t.Fatal("IP-B should be rate-limited after its own burst")
	}
}

// --- middleware tests ---

func TestMiddleware_429_RetryAfterHeader(t *testing.T) {
	lim := New(1, 1, false) // 1 rps, burst 1 (global)
	defer lim.Stop()

	mw := lim.Middleware()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	// Exhaust the single token.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", rec.Code)
	}

	// Next request should get 429 with Retry-After.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("want 429, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "1" {
		t.Errorf("Retry-After: want %q, got %q", "1", ra)
	}
}

func TestMiddleware_ExtractsIPFromRemoteAddr(t *testing.T) {
	lim := New(10, 1, true) // per-IP, burst 1
	defer lim.Stop()

	mw := lim.Middleware()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	// IP-A uses its token.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:9999"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP-A first request: want 200, got %d", rec.Code)
	}

	// IP-A second request should be limited.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:8888" // same IP, different port
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("IP-A second request: want 429, got %d", rec.Code)
	}

	// IP-B should still be allowed.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.2:9999"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("IP-B first request: want 200, got %d", rec.Code)
	}
}

// --- cleanup tests ---

func TestCleanup_EvictsOldEntries(t *testing.T) {
	lim := New(100, 10, true)
	defer lim.Stop()

	// Seed two IPs.
	lim.Allow("10.0.0.1")
	lim.Allow("10.0.0.2")

	// Manually backdate 10.0.0.1 to 6 minutes ago.
	val, ok := lim.perIP.Load("10.0.0.1")
	if !ok {
		t.Fatal("entry for 10.0.0.1 not found")
	}
	e := val.(*entry)
	e.lastSeen.Store(time.Now().Add(-6 * time.Minute).Unix())

	// Run the eviction logic directly (same as cleanup tick).
	cutoff := time.Now().Add(-5 * time.Minute).Unix()
	lim.perIP.Range(func(key, value any) bool {
		ent := value.(*entry)
		if ent.lastSeen.Load() < cutoff {
			lim.perIP.Delete(key)
		}
		return true
	})

	// 10.0.0.1 should be evicted; 10.0.0.2 should remain.
	if _, ok := lim.perIP.Load("10.0.0.1"); ok {
		t.Error("10.0.0.1 should have been evicted")
	}
	if _, ok := lim.perIP.Load("10.0.0.2"); !ok {
		t.Error("10.0.0.2 should still exist")
	}
}

// --- concurrency test ---

func TestLimiter_ConcurrentAccess(t *testing.T) {
	lim := New(1000, 100, true)
	defer lim.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				lim.Allow("10.0.0.1")
			}
		}()
	}
	wg.Wait()
	// If we get here without a race-detector panic, concurrency is safe.
}
