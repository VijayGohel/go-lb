package algo

import "github.com/VijayGohel/go-lb/internal/backend"

// LeastConnections picks the alive backend with the fewest active connections.
type LeastConnections struct{}

func (lc *LeastConnections) Name() string { return "least_connections" }

func (lc *LeastConnections) Next(backends []*backend.Backend) *backend.Backend {
	var best *backend.Backend
	var bestConns int64
	for _, b := range backends {
		if !b.IsAlive() {
			continue
		}
		conns := b.ActiveConns()
		if best == nil || conns < bestConns {
			best = b
			bestConns = conns
		}
	}
	return best
}
