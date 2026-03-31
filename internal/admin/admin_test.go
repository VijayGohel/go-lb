package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/VijayGohel/go-lb/internal/admin"
	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
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

func setup() (*pool.ServerPool, *proxy.LoadBalancer, http.Handler) {
	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	lb := proxy.New(p, rr)
	srv := admin.New(p, lb)
	return p, lb, srv.Handler()
}

func TestAdmin_ListBackends_Empty(t *testing.T) {
	_, _, h := setup()
	req := httptest.NewRequest(http.MethodGet, "/admin/backends", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(rw.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty array, got %d elements", len(result))
	}
}

func TestAdmin_ListBackends_WithBackends(t *testing.T) {
	p, _, h := setup()
	b := makeBackend("http://localhost:8081", true)
	b.Weight = 2
	p.AddBackend(b)

	req := httptest.NewRequest(http.MethodGet, "/admin/backends", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(rw.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(result))
	}
	if result[0]["url"] != "http://localhost:8081" {
		t.Errorf("url: want http://localhost:8081, got %v", result[0]["url"])
	}
	if result[0]["alive"] != true {
		t.Errorf("alive: want true, got %v", result[0]["alive"])
	}
}

func TestAdmin_AddBackend(t *testing.T) {
	p, _, h := setup()

	body := `{"url":"http://localhost:8082","weight":2}`
	req := httptest.NewRequest(http.MethodPost, "/admin/backends", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rw.Code, rw.Body.String())
	}
	found := false
	for _, b := range p.Backends() {
		if b.URL.String() == "http://localhost:8082" {
			found = true
			if b.Weight != 2 {
				t.Errorf("weight: want 2, got %d", b.Weight)
			}
		}
	}
	if !found {
		t.Fatal("added backend not found in pool")
	}
}

func TestAdmin_AddBackend_InvalidJSON_Returns400(t *testing.T) {
	_, _, h := setup()
	req := httptest.NewRequest(http.MethodPost, "/admin/backends", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rw.Code)
	}
}

func TestAdmin_AddBackend_Duplicate_Returns409(t *testing.T) {
	_, _, h := setup()

	body := `{"url":"http://localhost:8082","weight":1}`
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/backends", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
		return rw
	}

	if rw := post(); rw.Code != http.StatusCreated {
		t.Fatalf("first add: expected 201, got %d: %s", rw.Code, rw.Body.String())
	}
	if rw := post(); rw.Code != http.StatusConflict {
		t.Fatalf("duplicate add: expected 409, got %d: %s", rw.Code, rw.Body.String())
	}
}

func TestAdmin_RemoveBackend(t *testing.T) {
	p, _, h := setup()
	p.AddBackend(makeBackend("http://localhost:8081", true))

	encoded := url.QueryEscape("http://localhost:8081")
	req := httptest.NewRequest(http.MethodDelete, "/admin/backends/"+encoded, nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rw.Code, rw.Body.String())
	}
	if len(p.Backends()) != 0 {
		t.Fatal("backend not removed from pool")
	}
}

func TestAdmin_RemoveBackend_NotFound_Returns404(t *testing.T) {
	_, _, h := setup()
	encoded := url.QueryEscape("http://localhost:9999")
	req := httptest.NewRequest(http.MethodDelete, "/admin/backends/"+encoded, nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rw.Code)
	}
}

func TestAdmin_DisableBackend(t *testing.T) {
	p, _, h := setup()
	p.AddBackend(makeBackend("http://localhost:8081", true))

	encoded := url.QueryEscape("http://localhost:8081")
	req := httptest.NewRequest(http.MethodPost, "/admin/backends/"+encoded+"/disable", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
	}
	for _, b := range p.Backends() {
		if b.URL.String() == "http://localhost:8081" && b.IsAlive() {
			t.Fatal("backend should be disabled")
		}
	}
}

func TestAdmin_EnableBackend(t *testing.T) {
	p, _, h := setup()
	p.AddBackend(makeBackend("http://localhost:8081", false))

	encoded := url.QueryEscape("http://localhost:8081")
	req := httptest.NewRequest(http.MethodPost, "/admin/backends/"+encoded+"/enable", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
	}
	for _, b := range p.Backends() {
		if b.URL.String() == "http://localhost:8081" && !b.IsAlive() {
			t.Fatal("backend should be enabled")
		}
	}
}
