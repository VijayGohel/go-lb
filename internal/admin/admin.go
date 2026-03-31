package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
)

// Server is the admin HTTP server exposing pool management endpoints.
type Server struct {
	pool *pool.ServerPool
	lb   *proxy.LoadBalancer
}

// New creates an admin Server backed by the given pool and load balancer.
func New(p *pool.ServerPool, lb *proxy.LoadBalancer) *Server {
	return &Server{pool: p, lb: lb}
}

// Handler returns the ServeMux for the admin API.
// Use in tests to exercise handlers without binding a port.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/backends", s.listBackends)
	mux.HandleFunc("POST /admin/backends", s.addBackend)
	mux.HandleFunc("DELETE /admin/backends/{url}", s.removeBackend)
	mux.HandleFunc("POST /admin/backends/{url}/enable", s.enableBackend)
	mux.HandleFunc("POST /admin/backends/{url}/disable", s.disableBackend)
	return mux
}

// Start binds the admin server to addr and serves until ctx is cancelled.
// When ctx is cancelled, it shuts down the server with a 5-second drain timeout.
func (s *Server) Start(ctx context.Context, addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	return srv
}

type backendStatus struct {
	URL         string `json:"url"`
	Alive       bool   `json:"alive"`
	Weight      int    `json:"weight"`
	ActiveConns int64  `json:"active_conns"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) listBackends(w http.ResponseWriter, r *http.Request) {
	backends := s.pool.Backends()
	result := make([]backendStatus, len(backends))
	for i, b := range backends {
		result[i] = backendStatus{
			URL:         b.URL.String(),
			Alive:       b.IsAlive(),
			Weight:      b.Weight,
			ActiveConns: b.ActiveConns(),
		}
	}
	writeJSON(w, http.StatusOK, result)
}

type addBackendRequest struct {
	URL    string `json:"url"`
	Weight int    `json:"weight"`
}

func (s *Server) addBackend(w http.ResponseWriter, r *http.Request) {
	var req addBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid url: "+err.Error())
		return
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, "url must use http or https scheme with a non-empty host")
		return
	}
	weight := req.Weight
	if weight <= 0 {
		weight = 1
	}
	b := &backend.Backend{URL: u, Weight: weight}
	b.SetAlive(true)
	s.lb.SetupProxy(b)
	s.pool.AddBackend(b)
	writeJSON(w, http.StatusCreated, backendStatus{
		URL:    b.URL.String(),
		Alive:  true,
		Weight: b.Weight,
	})
}

func (s *Server) removeBackend(w http.ResponseWriter, r *http.Request) {
	rawURL, err := url.QueryUnescape(r.PathValue("url"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid url encoding")
		return
	}
	if !s.pool.Remove(rawURL) {
		writeError(w, http.StatusNotFound, "backend not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enableBackend(w http.ResponseWriter, r *http.Request) {
	s.setAlive(w, r, true)
}

func (s *Server) disableBackend(w http.ResponseWriter, r *http.Request) {
	s.setAlive(w, r, false)
}

func (s *Server) setAlive(w http.ResponseWriter, r *http.Request, alive bool) {
	rawURL, err := url.QueryUnescape(r.PathValue("url"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid url encoding")
		return
	}
	for _, b := range s.pool.Backends() {
		if b.URL.String() == rawURL {
			b.SetAlive(alive)
			writeJSON(w, http.StatusOK, backendStatus{
				URL:         b.URL.String(),
				Alive:       alive,
				Weight:      b.Weight,
				ActiveConns: b.ActiveConns(),
			})
			return
		}
	}
	writeError(w, http.StatusNotFound, "backend not found")
}
