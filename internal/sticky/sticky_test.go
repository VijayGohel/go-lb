package sticky_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VijayGohel/go-lb/internal/sticky"
)

func TestSetCookie_Attributes(t *testing.T) {
	a := sticky.New("golb_backend", 1*time.Hour, sticky.WithSecure(true))
	rw := httptest.NewRecorder()
	a.SetCookie(rw, "http://localhost:8081")

	cookies := rw.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]

	// Value should be base64-encoded backend URL
	decoded, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		t.Fatalf("cookie value is not valid base64: %v", err)
	}
	if string(decoded) != "http://localhost:8081" {
		t.Errorf("decoded value: want http://localhost:8081, got %s", decoded)
	}

	if c.Name != "golb_backend" {
		t.Errorf("name: want golb_backend, got %s", c.Name)
	}
	if c.Path != "/" {
		t.Errorf("path: want /, got %s", c.Path)
	}
	if c.MaxAge != 3600 {
		t.Errorf("max_age: want 3600, got %d", c.MaxAge)
	}
	if !c.HttpOnly {
		t.Error("want HttpOnly=true")
	}
	if !c.Secure {
		t.Error("want Secure=true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("want SameSite=Lax, got %d", c.SameSite)
	}
}

func TestFromRequest_Decodes(t *testing.T) {
	a := sticky.New("golb_backend", 1*time.Hour)
	encoded := base64.RawURLEncoding.EncodeToString([]byte("http://localhost:8081"))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "golb_backend", Value: encoded})

	got := a.FromRequest(req)
	if got != "http://localhost:8081" {
		t.Errorf("want http://localhost:8081, got %s", got)
	}
}

func TestFromRequest_EmptyCookie(t *testing.T) {
	a := sticky.New("golb_backend", 1*time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got := a.FromRequest(req)
	if got != "" {
		t.Errorf("want empty string for missing cookie, got %s", got)
	}
}

func TestFromRequest_InvalidBase64(t *testing.T) {
	a := sticky.New("golb_backend", 1*time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "golb_backend", Value: "%%%not-base64!!!"})

	got := a.FromRequest(req)
	if got != "" {
		t.Errorf("want empty string for invalid base64, got %s", got)
	}
}

func TestRoundTrip(t *testing.T) {
	a := sticky.New("golb_backend", 1*time.Hour)
	rw := httptest.NewRecorder()
	a.SetCookie(rw, "http://localhost:9090")

	// Build a request with the cookie that was set
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rw.Result().Cookies() {
		req.AddCookie(c)
	}

	got := a.FromRequest(req)
	if got != "http://localhost:9090" {
		t.Errorf("round-trip: want http://localhost:9090, got %s", got)
	}
}

func TestCustomName(t *testing.T) {
	a := sticky.New("my_session", 30*time.Minute)
	rw := httptest.NewRecorder()
	a.SetCookie(rw, "http://localhost:8081")

	cookies := rw.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != "my_session" {
		t.Errorf("name: want my_session, got %s", cookies[0].Name)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	got := a.FromRequest(req)
	if got != "http://localhost:8081" {
		t.Errorf("want http://localhost:8081, got %s", got)
	}
}

func TestWrongName(t *testing.T) {
	a := sticky.New("golb_backend", 1*time.Hour)
	encoded := base64.RawURLEncoding.EncodeToString([]byte("http://localhost:8081"))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "wrong_name", Value: encoded})

	got := a.FromRequest(req)
	if got != "" {
		t.Errorf("want empty string for wrong cookie name, got %s", got)
	}
}
