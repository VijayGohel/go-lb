package backend

import (
	"net/http/httputil"
	"net/url"
	"sync"
)

// Backend holds state for a single backend server.
// Access alive status only through SetAlive/IsAlive — never read the field directly.
type Backend struct {
	URL          *url.URL
	alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

// SetAlive updates the alive status thread-safely.
func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.alive = alive
	b.mux.Unlock()
}

// IsAlive returns the current alive status thread-safely.
func (b *Backend) IsAlive() (alive bool) {
	b.mux.RLock()
	alive = b.alive
	b.mux.RUnlock()
	return
}
