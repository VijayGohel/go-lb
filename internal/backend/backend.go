package backend

import (
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
)

// Backend holds state for a single backend server.
// Access alive status only through SetAlive/IsAlive — never read the field directly.
type Backend struct {
	URL          *url.URL
	Weight       int // Use GetWeight/SetWeight for concurrent access during hot reload.
	alive        bool
	activeConns  int64
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

// SetWeight updates the backend weight thread-safely.
func (b *Backend) SetWeight(w int) {
	b.mux.Lock()
	b.Weight = w
	b.mux.Unlock()
}

// GetWeight returns the backend weight thread-safely.
func (b *Backend) GetWeight() int {
	b.mux.RLock()
	w := b.Weight
	b.mux.RUnlock()
	return w
}

// IncrConns atomically increments the active connection count.
func (b *Backend) IncrConns() {
	atomic.AddInt64(&b.activeConns, 1)
}

// DecrConns atomically decrements the active connection count.
func (b *Backend) DecrConns() {
	atomic.AddInt64(&b.activeConns, -1)
}

// ActiveConns returns the current active connection count.
func (b *Backend) ActiveConns() int64 {
	return atomic.LoadInt64(&b.activeConns)
}
