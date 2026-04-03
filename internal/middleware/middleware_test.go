package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VijayGohel/go-lb/internal/middleware"
)

func TestChain_Empty_ReturnsOriginalHandler(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("inner"))
	})
	h := middleware.Chain(inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status: want %d, got %d", http.StatusOK, rec.Code)
	}
	if rec.Body.String() != "inner" {
		t.Errorf("body: want %q, got %q", "inner", rec.Body.String())
	}
}

func TestChain_SingleMiddleware(t *testing.T) {
	var order []string
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw")
			next.ServeHTTP(w, r)
		})
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "inner")
	})

	h := middleware.Chain(inner, mw)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if len(order) != 2 || order[0] != "mw" || order[1] != "inner" {
		t.Errorf("execution order: want [mw inner], got %v", order)
	}
}

func TestChain_Ordering_OutermostFirst(t *testing.T) {
	var order []string
	makeMW := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "inner")
	})

	// Chain(h, m1, m2) => m1(m2(h)), so m1 executes first.
	h := middleware.Chain(inner, makeMW("first"), makeMW("second"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first", "second", "inner"}
	if len(order) != len(want) {
		t.Fatalf("execution order length: want %d, got %d", len(want), len(order))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d]: want %q, got %q", i, want[i], order[i])
		}
	}
}
