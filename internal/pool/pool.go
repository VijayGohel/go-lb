package pool

import (
	"net/url"
	"sync"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/metrics"
)

// ServerPool holds registered backends, protected by an RWMutex.
type ServerPool struct {
	mu       sync.RWMutex
	backends []*backend.Backend
}

// AddBackend registers a backend with the pool.
// If a backend with the same URL is already registered it is a no-op.
func (s *ServerPool) AddBackend(b *backend.Backend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.backends {
		if existing.URL.String() == b.URL.String() {
			return
		}
	}
	s.backends = append(s.backends, b)
}

// Backends returns a snapshot copy of all registered backends.
// Callers must not modify the returned slice.
func (s *ServerPool) Backends() []*backend.Backend {
	s.mu.RLock()
	cp := append([]*backend.Backend(nil), s.backends...)
	s.mu.RUnlock()
	return cp
}

// Remove removes the backend with the given raw URL string.
// Returns true if found and removed, false otherwise.
func (s *ServerPool) Remove(rawURL string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, b := range s.backends {
		if b.URL.String() == rawURL {
			s.backends = append(s.backends[:i], s.backends[i+1:]...)
			return true
		}
	}
	return false
}

// MarkBackendStatus finds a backend by URL and updates its alive state.
// It also emits the backend_up metric so that ALL state-change paths
// (health checker, proxy error handler, admin enable/disable) are covered.
func (s *ServerPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.backends {
		if b.URL.String() == backendURL.String() {
			b.SetAlive(alive)
			metrics.SetBackendUp(backendURL.String(), alive)
			return
		}
	}
}
