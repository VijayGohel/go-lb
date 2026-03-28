package main

import (
	"net/http/httputil"
	"net/url"
	"sync"
)

// Backend holds state for a single backend server.
// Public fields and methods are frozen — see Interface Contracts.md before renaming.
type Backend struct {
	URL          *url.URL
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

// SetAlive updates the alive status thread-safely.
func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.Alive = alive
	b.mux.Unlock()
}

// IsAlive returns the current alive status thread-safely.
func (b *Backend) IsAlive() (alive bool) {
	b.mux.RLock()
	alive = b.Alive
	b.mux.RUnlock()
	return
}
