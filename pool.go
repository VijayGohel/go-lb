package main

import (
	"net/url"
	"sync/atomic"
)

// ServerPool holds registered backends and the round-robin counter.
// Public methods are frozen — see Interface Contracts.md before renaming.
type ServerPool struct {
	backends []*Backend
	current  uint64
}

// AddBackend registers a backend with the pool.
func (s *ServerPool) AddBackend(backend *Backend) {
	s.backends = append(s.backends, backend)
}

// NextIndex atomically increments and wraps the counter.
func (s *ServerPool) NextIndex() int {
	return int(atomic.AddUint64(&s.current, uint64(1)) % uint64(len(s.backends)))
}

// GetNextPeer returns the next alive backend using round-robin, skipping dead ones.
// Returns nil if no backends are alive.
func (s *ServerPool) GetNextPeer() *Backend {
	next := s.NextIndex()
	l := len(s.backends) + next
	for i := next; i < l; i++ {
		idx := i % len(s.backends)
		if s.backends[idx].IsAlive() {
			if i != next {
				atomic.StoreUint64(&s.current, uint64(idx))
			}
			return s.backends[idx]
		}
	}
	return nil
}

// MarkBackendStatus finds a backend by URL and updates its alive state.
func (s *ServerPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	for _, b := range s.backends {
		if b.URL.String() == backendURL.String() {
			b.SetAlive(alive)
			return
		}
	}
}
