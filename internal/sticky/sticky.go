package sticky

import (
	"encoding/base64"
	"math"
	"net/http"
	"time"
)

// maxAge returns the TTL as an integer number of seconds, rounded up to at
// least 1 so sub-second durations don't produce a session cookie (MaxAge=0).
func maxAge(ttl time.Duration) int {
	s := int(math.Ceil(ttl.Seconds()))
	if s < 1 {
		s = 1
	}
	return s
}

// Affinity manages cookie-based sticky sessions, mapping clients to backends
// via a base64-encoded cookie containing the backend URL.
type Affinity struct {
	cookieName string
	ttl        time.Duration
	secure     bool
}

// Option configures the Affinity.
type Option func(*Affinity)

// WithSecure sets the Secure flag on the sticky session cookie.
func WithSecure(secure bool) Option {
	return func(a *Affinity) { a.secure = secure }
}

// New creates an Affinity with the given cookie name and TTL.
func New(cookieName string, ttl time.Duration, opts ...Option) *Affinity {
	a := &Affinity{cookieName: cookieName, ttl: ttl}
	for _, o := range opts {
		o(a)
	}
	return a
}

// FromRequest reads the sticky session cookie and returns the backend URL it
// contains. Returns "" if the cookie is absent or cannot be decoded.
func (a *Affinity) FromRequest(r *http.Request) string {
	c, err := r.Cookie(a.cookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return ""
	}
	return string(raw)
}

// SetCookie writes a sticky session cookie containing the backend URL,
// base64-encoded to make it safe for the cookie header.
// Note: reversible, not confidential.
func (a *Affinity) SetCookie(w http.ResponseWriter, backendURL string) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(backendURL)),
		Path:     "/",
		MaxAge:   maxAge(a.ttl),
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
