package circuitbreaker_test

import (
	"sync"
	"testing"
	"time"

	"github.com/VijayGohel/go-lb/internal/circuitbreaker"
)

func TestClosedToOpen(t *testing.T) {
	cb := circuitbreaker.NewBreaker(3, 2, 50*time.Millisecond)

	for i := 0; i < 3; i++ {
		if !cb.Allow() {
			t.Fatalf("expected Allow=true in closed state, iteration %d", i)
		}
		cb.RecordFailure()
	}
	if cb.State() != circuitbreaker.Open {
		t.Fatalf("expected Open after %d failures, got %s", 3, cb.State())
	}
}

func TestOpenRejects(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 1, 100*time.Millisecond)
	cb.RecordFailure() // → Open

	if cb.Allow() {
		t.Fatal("expected Allow=false in Open state")
	}
}

func TestOpenToHalfOpen(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 1, 20*time.Millisecond)
	cb.RecordFailure() // → Open

	time.Sleep(30 * time.Millisecond)

	if !cb.Allow() {
		t.Fatal("expected Allow=true after timeout (HalfOpen)")
	}
	if cb.State() != circuitbreaker.HalfOpen {
		t.Fatalf("expected HalfOpen, got %s", cb.State())
	}
}

func TestHalfOpenToClosed(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 2, 20*time.Millisecond)
	cb.RecordFailure() // → Open

	time.Sleep(30 * time.Millisecond)
	cb.Allow() // → HalfOpen, consume permit

	cb.RecordSuccess() // 1st success, still HalfOpen
	if cb.State() != circuitbreaker.HalfOpen {
		t.Fatalf("expected HalfOpen after 1 success, got %s", cb.State())
	}

	cb.Allow()         // consume second permit
	cb.RecordSuccess() // 2nd success → Closed
	if cb.State() != circuitbreaker.Closed {
		t.Fatalf("expected Closed after 2 successes, got %s", cb.State())
	}
}

func TestHalfOpenToOpen(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 2, 20*time.Millisecond)
	cb.RecordFailure() // → Open

	time.Sleep(30 * time.Millisecond)
	cb.Allow() // → HalfOpen

	cb.RecordFailure() // → back to Open
	if cb.State() != circuitbreaker.Open {
		t.Fatalf("expected Open after HalfOpen failure, got %s", cb.State())
	}
}

func TestHalfOpenAllowsOneProbe(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 1, 20*time.Millisecond)
	cb.RecordFailure() // → Open

	time.Sleep(30 * time.Millisecond)

	if !cb.Allow() {
		t.Fatal("first Allow in HalfOpen should be true")
	}
	if cb.Allow() {
		t.Fatal("second Allow in HalfOpen should be false (permit consumed)")
	}
}

func TestClosedResetsFailuresOnSuccess(t *testing.T) {
	cb := circuitbreaker.NewBreaker(3, 1, 50*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // resets counter

	cb.RecordFailure()
	cb.RecordFailure()
	// Only 2 consecutive failures, not 3
	if cb.State() != circuitbreaker.Closed {
		t.Fatalf("expected Closed, got %s", cb.State())
	}
}

func TestRegistryLazyCreates(t *testing.T) {
	r := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
	})

	cb1 := r.Get("http://localhost:8081")
	cb2 := r.Get("http://localhost:8081")
	if cb1 != cb2 {
		t.Fatal("expected same breaker for same URL")
	}

	cb3 := r.Get("http://localhost:8082")
	if cb1 == cb3 {
		t.Fatal("expected different breaker for different URL")
	}
}

func TestRegistryRemove(t *testing.T) {
	r := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          30 * time.Second,
	})

	cb1 := r.Get("http://localhost:8081")
	cb1.RecordFailure() // → Open

	r.Remove("http://localhost:8081")
	cb2 := r.Get("http://localhost:8081")
	if cb2.State() != circuitbreaker.Closed {
		t.Fatal("expected fresh Closed breaker after Remove")
	}
}

func TestConcurrentAccess(t *testing.T) {
	cb := circuitbreaker.NewBreaker(100, 1, 50*time.Millisecond)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.Allow()
			cb.RecordFailure()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.Allow()
			cb.RecordSuccess()
		}()
	}
	wg.Wait()
	// Just verify no panics or deadlocks
}

func TestStateString(t *testing.T) {
	tests := []struct {
		s    circuitbreaker.State
		want string
	}{
		{circuitbreaker.Closed, "closed"},
		{circuitbreaker.Open, "open"},
		{circuitbreaker.HalfOpen, "half-open"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestEligible_Closed(t *testing.T) {
	cb := circuitbreaker.NewBreaker(3, 2, 50*time.Millisecond)
	if !cb.Eligible() {
		t.Fatal("Closed breaker should be eligible")
	}
}

func TestEligible_Open_BeforeTimeout(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 1, 100*time.Millisecond)
	cb.RecordFailure() // → Open
	if cb.Eligible() {
		t.Fatal("Open breaker before timeout should NOT be eligible")
	}
}

func TestEligible_Open_AfterTimeout(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 1, 20*time.Millisecond)
	cb.RecordFailure() // → Open
	time.Sleep(30 * time.Millisecond)
	if !cb.Eligible() {
		t.Fatal("Open breaker after timeout should be eligible")
	}
	// Eligible must NOT transition state — still Open until Allow() is called.
	if cb.State() != circuitbreaker.Open {
		t.Fatalf("Eligible should not mutate state, got %s", cb.State())
	}
}

func TestEligible_HalfOpen(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 1, 20*time.Millisecond)
	cb.RecordFailure() // → Open
	time.Sleep(30 * time.Millisecond)
	cb.Allow() // → HalfOpen, consume permit
	if !cb.Eligible() {
		t.Fatal("HalfOpen breaker should be eligible")
	}
}

func TestEligible_DoesNotConsumePermit(t *testing.T) {
	cb := circuitbreaker.NewBreaker(1, 1, 20*time.Millisecond)
	cb.RecordFailure() // → Open
	time.Sleep(30 * time.Millisecond)

	// Call Eligible multiple times — it should not consume the half-open permit.
	for i := 0; i < 5; i++ {
		if !cb.Eligible() {
			t.Fatalf("Eligible() returned false on call %d", i)
		}
	}
	// Now Allow() should still succeed and transition to HalfOpen.
	if !cb.Allow() {
		t.Fatal("Allow() should succeed after multiple Eligible() calls")
	}
	if cb.State() != circuitbreaker.HalfOpen {
		t.Fatalf("expected HalfOpen after Allow(), got %s", cb.State())
	}
}

func TestNewBreaker_Clamping(t *testing.T) {
	// Verify that thresholds and timeout are clamped to minimums.
	cb := circuitbreaker.NewBreaker(0, 0, 0)
	// With failureThreshold clamped to 1, a single failure should open.
	cb.RecordFailure()
	if cb.State() != circuitbreaker.Open {
		t.Fatalf("expected Open with clamped failureThreshold=1, got %s", cb.State())
	}
}
