# GoLB V1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade GoLB from MVP (round-robin only, CLI-config) to V1 with pluggable algorithms, YAML config, health check thresholds, and an admin REST API.

**Architecture:** Strategy pattern for algorithms (`internal/algo`), YAML + CLI merge in `internal/config`, threshold state machine in `internal/health`, and a new `internal/admin` HTTP server on a separate port. `proxy.LoadBalancer` gains an injected `algo.Algorithm` and tracks active connections per request.

**Tech Stack:** Go 1.25.5, `gopkg.in/yaml.v3`, standard library only otherwise.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/backend/backend.go` | Modify | Add `Weight int`, `activeConns int64`, `IncrConns/DecrConns/ActiveConns` |
| `internal/pool/pool.go` | Modify | Add `sync.RWMutex`, `Remove(rawURL string) bool`, remove `GetNextPeer/NextIndex/current` |
| `internal/pool/pool_test.go` | Modify | Remove `GetNextPeer` tests; add `Remove` test |
| `internal/algo/algo.go` | Create | `Algorithm` interface + `New(name string)` factory |
| `internal/algo/round_robin.go` | Create | `RoundRobin` — atomic counter |
| `internal/algo/least_connections.go` | Create | `LeastConnections` — fewest active conns |
| `internal/algo/weighted_round_robin.go` | Create | `WeightedRoundRobin` — smooth Nginx WRR |
| `internal/algo/algo_test.go` | Create | Tests for all three algorithms |
| `internal/proxy/proxy.go` | Modify | Hold `algo.Algorithm`; call `Next()` + `IncrConns/DecrConns` |
| `internal/proxy/proxy_test.go` | Modify | Pass algo to `proxy.New` |
| `internal/config/config.go` | Create | `Config` struct, `Defaults()`, `Load(path, args)` |
| `internal/config/config_test.go` | Create | Tests for defaults, YAML parse, CLI override |
| `internal/health/health.go` | Modify | Add threshold counters + state machine |
| `internal/health/health_test.go` | Modify | Update constructor call; add threshold tests |
| `internal/admin/admin.go` | Create | `Server` struct, `Handler()`, 5 REST endpoints |
| `internal/admin/admin_test.go` | Create | HTTP tests for all 5 endpoints |
| `cmd/golb/main.go` | Modify | Use `config.Load`, wire algo + admin, dual-server shutdown |
| `go.mod` / `go.sum` | Modify | Add `gopkg.in/yaml.v3` |

---

### Task 1: Add yaml.v3 dependency + create feature branch

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Create feature branch**

```bash
cd ~/GoLB/golb
git checkout -b feat/golb-v1
```

- [ ] **Step 2: Add yaml.v3**

```bash
go get gopkg.in/yaml.v3
```

- [ ] **Step 3: Verify go.mod updated**

```bash
grep yaml go.mod
```
Expected: `gopkg.in/yaml.v3 v3.x.x`

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add gopkg.in/yaml.v3 dependency"
```

---

### Task 2: Extend `internal/backend` — Weight + active connections

**Files:**
- Modify: `internal/backend/backend.go`

- [ ] **Step 1: Write the failing test**

Create `internal/backend/backend_test.go`:

```go
package backend_test

import (
	"net/url"
	"testing"

	"github.com/VijayGohel/go-lb/internal/backend"
)

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func TestBackend_ActiveConns(t *testing.T) {
	b := &backend.Backend{URL: mustParseURL("http://localhost:8081"), Weight: 1}
	b.SetAlive(true)

	if b.ActiveConns() != 0 {
		t.Fatalf("expected 0 active conns, got %d", b.ActiveConns())
	}

	b.IncrConns()
	b.IncrConns()
	if b.ActiveConns() != 2 {
		t.Fatalf("expected 2 active conns after 2 IncrConns, got %d", b.ActiveConns())
	}

	b.DecrConns()
	if b.ActiveConns() != 1 {
		t.Fatalf("expected 1 active conn after DecrConns, got %d", b.ActiveConns())
	}
}

func TestBackend_WeightDefault(t *testing.T) {
	b := &backend.Backend{URL: mustParseURL("http://localhost:8081")}
	// Weight zero-value is 0; the algo layer normalises it to 1. Just verify field exists.
	b.Weight = 3
	if b.Weight != 3 {
		t.Fatalf("expected Weight=3, got %d", b.Weight)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/backend/...
```
Expected: FAIL — `b.IncrConns undefined`

- [ ] **Step 3: Add fields and methods to `internal/backend/backend.go`**

```go
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
	Weight       int
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/backend/...
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/backend/
git commit -m "feat(backend): add Weight field and atomic active-connection tracking"
```

---

### Task 3: Harden `internal/pool` — RWMutex + Remove

**Files:**
- Modify: `internal/pool/pool.go`
- Modify: `internal/pool/pool_test.go`

- [ ] **Step 1: Write failing tests for Remove and thread-safety**

Add to `internal/pool/pool_test.go` (keep existing tests, add below):

```go
func TestServerPool_Remove_ExistingBackend(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))
	p.AddBackend(makeBackend("http://localhost:8082", true))

	removed := p.Remove("http://localhost:8081")
	if !removed {
		t.Fatal("Remove should return true for existing backend")
	}
	for _, b := range p.Backends() {
		if b.URL.String() == "http://localhost:8081" {
			t.Fatal("removed backend still in pool")
		}
	}
}

func TestServerPool_Remove_MissingBackend(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))

	removed := p.Remove("http://localhost:9999")
	if removed {
		t.Fatal("Remove should return false for unknown backend")
	}
	if len(p.Backends()) != 1 {
		t.Fatal("pool length changed after removing non-existent backend")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/pool/...
```
Expected: FAIL — `p.Remove undefined`

- [ ] **Step 3: Rewrite `internal/pool/pool.go`**

```go
package pool

import (
	"net/url"
	"sync"

	"github.com/VijayGohel/go-lb/internal/backend"
)

// ServerPool holds registered backends, protected by an RWMutex.
type ServerPool struct {
	mu       sync.RWMutex
	backends []*backend.Backend
}

// AddBackend registers a backend with the pool.
func (s *ServerPool) AddBackend(b *backend.Backend) {
	s.mu.Lock()
	s.backends = append(s.backends, b)
	s.mu.Unlock()
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
func (s *ServerPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.backends {
		if b.URL.String() == backendURL.String() {
			b.SetAlive(alive)
			return
		}
	}
}
```

- [ ] **Step 4: Update `internal/pool/pool_test.go` — remove `GetNextPeer` tests**

The existing tests reference `p.GetNextPeer()` which no longer exists. Replace the entire test file:

```go
package pool_test

import (
	"net/url"
	"testing"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/pool"
)

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func makeBackend(rawURL string, alive bool) *backend.Backend {
	b := &backend.Backend{URL: mustParseURL(rawURL)}
	b.SetAlive(alive)
	return b
}

func TestServerPool_Backends_ReturnsCopy(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))
	p.AddBackend(makeBackend("http://localhost:8082", true))

	snap := p.Backends()
	if len(snap) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(snap))
	}
	// Mutating the snapshot must not affect the pool.
	snap[0] = nil
	if p.Backends()[0] == nil {
		t.Fatal("mutating snapshot modified the pool's internal slice")
	}
}

func TestServerPool_MarkBackendStatus(t *testing.T) {
	p := &pool.ServerPool{}
	b := makeBackend("http://localhost:8081", true)
	p.AddBackend(b)

	p.MarkBackendStatus(mustParseURL("http://localhost:8081"), false)

	if b.IsAlive() {
		t.Fatal("backend should be marked dead")
	}
}

func TestServerPool_Remove_ExistingBackend(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))
	p.AddBackend(makeBackend("http://localhost:8082", true))

	removed := p.Remove("http://localhost:8081")
	if !removed {
		t.Fatal("Remove should return true for existing backend")
	}
	for _, b := range p.Backends() {
		if b.URL.String() == "http://localhost:8081" {
			t.Fatal("removed backend still in pool")
		}
	}
}

func TestServerPool_Remove_MissingBackend(t *testing.T) {
	p := &pool.ServerPool{}
	p.AddBackend(makeBackend("http://localhost:8081", true))

	removed := p.Remove("http://localhost:9999")
	if removed {
		t.Fatal("Remove should return false for unknown backend")
	}
	if len(p.Backends()) != 1 {
		t.Fatal("pool length changed after removing non-existent backend")
	}
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/pool/... ./internal/backend/...
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/pool/
git commit -m "feat(pool): add RWMutex protection and Remove method; drop GetNextPeer"
```

---

### Task 4: `internal/algo` — interface + RoundRobin

**Files:**
- Create: `internal/algo/algo.go`
- Create: `internal/algo/round_robin.go`
- Create: `internal/algo/algo_test.go` (partial — RoundRobin tests only)

- [ ] **Step 1: Write failing test**

Create `internal/algo/algo_test.go`:

```go
package algo_test

import (
	"net/url"
	"testing"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
)

func makeBackend(rawURL string, alive bool) *backend.Backend {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	b := &backend.Backend{URL: u, Weight: 1}
	b.SetAlive(alive)
	return b
}

func TestNew_UnknownAlgorithm(t *testing.T) {
	_, err := algo.New("magic")
	if err == nil {
		t.Fatal("expected error for unknown algorithm name")
	}
}

func TestRoundRobin_Name(t *testing.T) {
	a, err := algo.New("round_robin")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "round_robin" {
		t.Fatalf("expected name round_robin, got %s", a.Name())
	}
}

func TestRoundRobin_DistributesEvenly(t *testing.T) {
	backends := []*backend.Backend{
		makeBackend("http://localhost:8081", true),
		makeBackend("http://localhost:8082", true),
		makeBackend("http://localhost:8083", true),
	}
	a, _ := algo.New("round_robin")
	hits := map[string]int{}
	for i := 0; i < 9; i++ {
		b := a.Next(backends)
		if b == nil {
			t.Fatalf("iteration %d: Next returned nil", i)
		}
		hits[b.URL.String()]++
	}
	for u, count := range hits {
		if count != 3 {
			t.Errorf("backend %s got %d hits, want 3", u, count)
		}
	}
}

func TestRoundRobin_SkipsDeadBackends(t *testing.T) {
	backends := []*backend.Backend{
		makeBackend("http://localhost:8081", false),
		makeBackend("http://localhost:8082", true),
		makeBackend("http://localhost:8083", false),
	}
	a, _ := algo.New("round_robin")
	for i := 0; i < 6; i++ {
		b := a.Next(backends)
		if b == nil {
			t.Fatal("Next returned nil when one backend is alive")
		}
		if b.URL.String() != "http://localhost:8082" {
			t.Errorf("iteration %d: expected only alive backend, got %s", i, b.URL.String())
		}
	}
}

func TestRoundRobin_AllDead_ReturnsNil(t *testing.T) {
	backends := []*backend.Backend{
		makeBackend("http://localhost:8081", false),
		makeBackend("http://localhost:8082", false),
	}
	a, _ := algo.New("round_robin")
	if b := a.Next(backends); b != nil {
		t.Fatalf("expected nil when all dead, got %s", b.URL.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/algo/...
```
Expected: FAIL — package not found

- [ ] **Step 3: Create `internal/algo/algo.go`**

```go
package algo

import (
	"fmt"

	"github.com/VijayGohel/go-lb/internal/backend"
)

// Algorithm selects the next backend from a slice of alive candidates.
type Algorithm interface {
	Next(backends []*backend.Backend) *backend.Backend
	Name() string
}

// New returns the Algorithm for the given name, or an error if unknown.
// Valid names: "round_robin", "least_connections", "weighted_round_robin".
func New(name string) (Algorithm, error) {
	switch name {
	case "round_robin":
		return &RoundRobin{}, nil
	case "least_connections":
		return &LeastConnections{}, nil
	case "weighted_round_robin":
		return NewWeightedRoundRobin(), nil
	default:
		return nil, fmt.Errorf("unknown algorithm %q: must be round_robin, least_connections, or weighted_round_robin", name)
	}
}
```

- [ ] **Step 4: Create `internal/algo/round_robin.go`**

```go
package algo

import (
	"sync/atomic"

	"github.com/VijayGohel/go-lb/internal/backend"
)

// RoundRobin distributes requests evenly across alive backends using an atomic counter.
type RoundRobin struct {
	current uint64
}

func (rr *RoundRobin) Name() string { return "round_robin" }

// Next returns the next alive backend in round-robin order.
// Returns nil if no backends are alive.
func (rr *RoundRobin) Next(backends []*backend.Backend) *backend.Backend {
	n := len(backends)
	if n == 0 {
		return nil
	}
	next := int(atomic.AddUint64(&rr.current, 1) % uint64(n))
	for i := 0; i < n; i++ {
		idx := (next + i) % n
		if backends[idx].IsAlive() {
			atomic.StoreUint64(&rr.current, uint64(idx))
			return backends[idx]
		}
	}
	return nil
}
```

- [ ] **Step 5: Run RoundRobin tests**

```bash
go test ./internal/algo/... -run TestRoundRobin -run TestNew
```
Expected: PASS (LeastConnections/WRR not yet wired, but `New` returns error for them which is acceptable — skip those if they exist)

- [ ] **Step 6: Commit**

```bash
git add internal/algo/
git commit -m "feat(algo): add Algorithm interface, factory, and RoundRobin implementation"
```

---

### Task 5: `internal/algo` — LeastConnections

**Files:**
- Create: `internal/algo/least_connections.go`
- Modify: `internal/algo/algo_test.go` (add LC tests)

- [ ] **Step 1: Add failing tests to `internal/algo/algo_test.go`**

Append to the existing test file:

```go
func TestLeastConnections_Name(t *testing.T) {
	a, err := algo.New("least_connections")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "least_connections" {
		t.Fatalf("expected least_connections, got %s", a.Name())
	}
}

func TestLeastConnections_PicksFewestConns(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b2 := makeBackend("http://localhost:8082", true)
	b3 := makeBackend("http://localhost:8083", true)
	b1.IncrConns()
	b1.IncrConns() // b1 has 2
	b2.IncrConns() // b2 has 1
	// b3 has 0

	a, _ := algo.New("least_connections")
	got := a.Next([]*backend.Backend{b1, b2, b3})
	if got == nil {
		t.Fatal("Next returned nil")
	}
	if got.URL.String() != "http://localhost:8083" {
		t.Errorf("expected b3 (fewest conns), got %s", got.URL.String())
	}
}

func TestLeastConnections_TieBrokenByOrder(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b2 := makeBackend("http://localhost:8082", true)
	// both have 0 conns

	a, _ := algo.New("least_connections")
	got := a.Next([]*backend.Backend{b1, b2})
	if got.URL.String() != "http://localhost:8081" {
		t.Errorf("tie should be broken by registration order (first wins), got %s", got.URL.String())
	}
}

func TestLeastConnections_SkipsDeadBackends(t *testing.T) {
	dead := makeBackend("http://localhost:8081", false)
	alive := makeBackend("http://localhost:8082", true)
	alive.IncrConns()
	alive.IncrConns()
	alive.IncrConns() // more conns than dead, but dead should be skipped

	a, _ := algo.New("least_connections")
	got := a.Next([]*backend.Backend{dead, alive})
	if got == nil {
		t.Fatal("Next returned nil when one backend is alive")
	}
	if got.URL.String() != "http://localhost:8082" {
		t.Errorf("expected alive backend, got %s", got.URL.String())
	}
}
```

- [ ] **Step 2: Run to verify fail**

```bash
go test ./internal/algo/... -run TestLeastConnections
```
Expected: FAIL — `LeastConnections undefined`

- [ ] **Step 3: Create `internal/algo/least_connections.go`**

```go
package algo

import (
	"github.com/VijayGohel/go-lb/internal/backend"
)

// LeastConnections picks the alive backend with the fewest active connections.
// Ties are broken by order of registration (first in slice wins).
type LeastConnections struct{}

func (lc *LeastConnections) Name() string { return "least_connections" }

// Next iterates all backends and returns the alive one with the lowest ActiveConns count.
// Returns nil if no backends are alive.
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
```

- [ ] **Step 4: Run all algo tests**

```bash
go test ./internal/algo/...
```
Expected: PASS (WRR tests not written yet)

- [ ] **Step 5: Commit**

```bash
git add internal/algo/
git commit -m "feat(algo): add LeastConnections implementation"
```

---

### Task 6: `internal/algo` — WeightedRoundRobin

**Files:**
- Create: `internal/algo/weighted_round_robin.go`
- Modify: `internal/algo/algo_test.go` (add WRR tests)

- [ ] **Step 1: Add failing WRR tests to `internal/algo/algo_test.go`**

```go
func TestWeightedRoundRobin_Name(t *testing.T) {
	a, err := algo.New("weighted_round_robin")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "weighted_round_robin" {
		t.Fatalf("expected weighted_round_robin, got %s", a.Name())
	}
}

func TestWeightedRoundRobin_DistributesProportionally(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b1.Weight = 3
	b2 := makeBackend("http://localhost:8082", true)
	b2.Weight = 1

	a, _ := algo.New("weighted_round_robin")
	hits := map[string]int{}
	// 8 requests: b1 should get 6, b2 should get 2
	for i := 0; i < 8; i++ {
		b := a.Next([]*backend.Backend{b1, b2})
		if b == nil {
			t.Fatalf("iteration %d: Next returned nil", i)
		}
		hits[b.URL.String()]++
	}
	if hits["http://localhost:8081"] != 6 {
		t.Errorf("b1 (weight=3) expected 6 hits, got %d", hits["http://localhost:8081"])
	}
	if hits["http://localhost:8082"] != 2 {
		t.Errorf("b2 (weight=1) expected 2 hits, got %d", hits["http://localhost:8082"])
	}
}

func TestWeightedRoundRobin_ZeroWeightTreatedAsOne(t *testing.T) {
	b1 := makeBackend("http://localhost:8081", true)
	b1.Weight = 0 // should be treated as 1
	b2 := makeBackend("http://localhost:8082", true)
	b2.Weight = 0

	a, _ := algo.New("weighted_round_robin")
	hits := map[string]int{}
	for i := 0; i < 6; i++ {
		b := a.Next([]*backend.Backend{b1, b2})
		if b == nil {
			t.Fatalf("iteration %d: Next returned nil", i)
		}
		hits[b.URL.String()]++
	}
	if hits["http://localhost:8081"] != 3 || hits["http://localhost:8082"] != 3 {
		t.Errorf("equal-weight backends should get equal hits, got %v", hits)
	}
}

func TestWeightedRoundRobin_SkipsDeadBackends(t *testing.T) {
	dead := makeBackend("http://localhost:8081", false)
	dead.Weight = 5
	alive := makeBackend("http://localhost:8082", true)
	alive.Weight = 1

	a, _ := algo.New("weighted_round_robin")
	for i := 0; i < 4; i++ {
		b := a.Next([]*backend.Backend{dead, alive})
		if b == nil || b.URL.String() != "http://localhost:8082" {
			t.Fatalf("iteration %d: should only pick alive backend", i)
		}
	}
}
```

- [ ] **Step 2: Run to verify fail**

```bash
go test ./internal/algo/... -run TestWeightedRoundRobin
```
Expected: FAIL — `NewWeightedRoundRobin undefined`

- [ ] **Step 3: Create `internal/algo/weighted_round_robin.go`**

```go
package algo

import (
	"sync"

	"github.com/VijayGohel/go-lb/internal/backend"
)

// WeightedRoundRobin implements the smooth weighted round-robin algorithm (Nginx).
// Each call to Next increases every backend's currentWeight by its Weight, then
// selects the highest and subtracts the total weight from it.
type WeightedRoundRobin struct {
	mu             sync.Mutex
	currentWeights map[string]int
}

// NewWeightedRoundRobin initialises the algorithm with an empty weight map.
func NewWeightedRoundRobin() *WeightedRoundRobin {
	return &WeightedRoundRobin{currentWeights: make(map[string]int)}
}

func (w *WeightedRoundRobin) Name() string { return "weighted_round_robin" }

// effectiveWeight returns the configured weight, treating <= 0 as 1.
func effectiveWeight(b *backend.Backend) int {
	if b.Weight <= 0 {
		return 1
	}
	return b.Weight
}

// Next applies the smooth WRR algorithm over alive backends.
// Returns nil if no backends are alive.
func (w *WeightedRoundRobin) Next(backends []*backend.Backend) *backend.Backend {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Build alive-only slice and compute total weight.
	var alive []*backend.Backend
	total := 0
	for _, b := range backends {
		if b.IsAlive() {
			alive = append(alive, b)
			total += effectiveWeight(b)
		}
	}
	if len(alive) == 0 {
		return nil
	}

	// Increase each backend's currentWeight by its effective weight.
	for _, b := range alive {
		key := b.URL.String()
		w.currentWeights[key] += effectiveWeight(b)
	}

	// Pick the backend with the highest currentWeight.
	var best *backend.Backend
	for _, b := range alive {
		key := b.URL.String()
		if best == nil || w.currentWeights[key] > w.currentWeights[best.URL.String()] {
			best = b
		}
	}

	// Subtract total weight from the selected backend.
	w.currentWeights[best.URL.String()] -= total
	return best
}
```

- [ ] **Step 4: Run all algo tests**

```bash
go test ./internal/algo/...
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/algo/
git commit -m "feat(algo): add WeightedRoundRobin (smooth Nginx algorithm)"
```

---

### Task 7: Wire algo into `internal/proxy`

**Files:**
- Modify: `internal/proxy/proxy.go`
- Modify: `internal/proxy/proxy_test.go`

- [ ] **Step 1: Update `internal/proxy/proxy.go`**

Change `LoadBalancer` to hold an `algo.Algorithm` and call `IncrConns/DecrConns`:

```go
package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/pool"
)

const (
	// MaxBackendSwitches is the maximum number of backend switches allowed per request.
	MaxBackendSwitches = 3
	maxRetries         = 3 // per-backend retries before marking dead and switching
)

// idempotentMethods are safe to retry — repeating them has no observable side effects.
var idempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// contextKey is a private type for context values to avoid collisions.
type contextKey int

const (
	attemptsKey  contextKey = iota // number of backend switches so far (0-based)
	retryKey                       // number of retries on the current backend
	requestIDKey                   // stable ID propagated across backend switches
)

// LoadBalancer routes requests across a pool of backends using the configured algorithm.
// It implements http.Handler.
type LoadBalancer struct {
	pool *pool.ServerPool
	algo algo.Algorithm
}

// New creates a LoadBalancer backed by the given pool and algorithm.
func New(p *pool.ServerPool, a algo.Algorithm) *LoadBalancer {
	return &LoadBalancer{pool: p, algo: a}
}

func getAttemptsFromContext(r *http.Request) int {
	if v, ok := r.Context().Value(attemptsKey).(int); ok {
		return v
	}
	return 0
}

func getRetryFromContext(r *http.Request) int {
	if v, ok := r.Context().Value(retryKey).(int); ok {
		return v
	}
	return 0
}

// getOrCreateRequestID returns the stable request ID from context, creating one if absent.
// Falls back to a timestamp-based ID if crypto/rand fails.
func getOrCreateRequestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey).(string); ok {
		return id
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		slog.Warn("rand_read_failed", "error", err.Error())
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// SetupProxy attaches a retry-aware error handler to the backend's ReverseProxy.
// Call this for every backend before adding it to the pool.
func (lb *LoadBalancer) SetupProxy(b *backend.Backend) {
	proxy := httputil.NewSingleHostReverseProxy(b.URL)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		retry := getRetryFromContext(r)
		if retry < maxRetries && idempotentMethods[r.Method] {
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-timer.C:
				ctx := context.WithValue(r.Context(), retryKey, retry+1)
				proxy.ServeHTTP(w, r.WithContext(ctx))
			case <-r.Context().Done():
				timer.Stop()
				return
			}
			return
		}
		lb.pool.MarkBackendStatus(b.URL, false)
		slog.Warn("backend_down", "backend", b.URL.String(), "error", e.Error())

		attempts := getAttemptsFromContext(r)
		if attempts < MaxBackendSwitches {
			ctx := context.WithValue(r.Context(), attemptsKey, attempts+1)
			ctx = context.WithValue(ctx, retryKey, 0)
			lb.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
	}
	b.ReverseProxy = proxy
}

// ServeHTTP picks the next backend via the algorithm and forwards the request.
// It tracks active connections per backend for the least-connections algorithm.
// It implements http.Handler.
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	attempts := getAttemptsFromContext(r)
	if attempts >= MaxBackendSwitches {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}
	peer := lb.algo.Next(lb.pool.Backends())
	if peer == nil {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	peer.IncrConns()
	defer peer.DecrConns()

	requestID := getOrCreateRequestID(r)
	if _, ok := r.Context().Value(requestIDKey).(string); !ok {
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID))
	}

	start := time.Now()
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	peer.ReverseProxy.ServeHTTP(rw, r)
	slog.Info("request",
		"request_id", requestID,
		"backend", peer.URL.String(),
		"latency_ms", time.Since(start).Milliseconds(),
		"status", rw.statusCode,
		"attempt", attempts+1,
	)
}

// responseWriter wraps http.ResponseWriter to capture the status code for logging.
// It forwards optional interfaces (Flusher, Hijacker) so streaming and WebSocket
// upgrades continue to work through the reverse proxy.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}
```

- [ ] **Step 2: Update `internal/proxy/proxy_test.go` — pass algo to `proxy.New`**

Change `newLB` helper and any direct `proxy.New(p)` calls to `proxy.New(p, rr)`:

```go
package proxy_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

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

// newLB wires backends into a pool with round-robin and returns a ready LoadBalancer.
func newLB(backends ...*backend.Backend) *proxy.LoadBalancer {
	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	lb := proxy.New(p, rr)
	for _, b := range backends {
		lb.SetupProxy(b)
		p.AddBackend(b)
	}
	return lb
}

func TestLb_ProxiesToAliveBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	lb := newLB(makeBackend(srv.URL, true))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
}

func TestLb_Returns503_WhenNoBackendsAlive(t *testing.T) {
	lb := newLB(makeBackend("http://localhost:19998", false))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rw.Code)
	}
}

func TestLb_RoundRobin(t *testing.T) {
	var hits [3]int64
	backends := make([]*httptest.Server, 3)
	for i := range backends {
		i := i
		backends[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&hits[i], 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer backends[i].Close()
	}

	p := &pool.ServerPool{}
	rr, _ := algo.New("round_robin")
	lb := proxy.New(p, rr)
	for _, srv := range backends {
		b := makeBackend(srv.URL, true)
		lb.SetupProxy(b)
		p.AddBackend(b)
	}

	for i := 0; i < 9; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rw := httptest.NewRecorder()
		lb.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rw.Code)
		}
	}
	for i, h := range hits {
		if h != 3 {
			t.Errorf("backend %d got %d requests, want 3", i, h)
		}
	}
}

func TestLb_SwitchesBackend_OnFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	lb := newLB(makeBackend(deadAddr, true), makeBackend(alive.URL, true))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Errorf("expected 200 after switching to alive backend, got %d", rw.Code)
	}
}

func TestLb_AllBackendsFail_Returns503(t *testing.T) {
	var deadBackends []*backend.Backend
	for i := 0; i < proxy.MaxBackendSwitches; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		deadBackends = append(deadBackends, makeBackend("http://"+ln.Addr().String(), true))
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
	}

	lb := newLB(deadBackends...)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	lb.ServeHTTP(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when all backends fail, got %d", rw.Code)
	}
}

func TestLb_DeadBackendSkipped(t *testing.T) {
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alive.Close()

	lb := newLB(
		makeBackend("http://localhost:19997", false),
		makeBackend(alive.URL, true),
	)

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rw := httptest.NewRecorder()
		lb.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d — dead backend not skipped", i, rw.Code)
		}
	}
}
```

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/ internal/algo/
git commit -m "feat(proxy): wire Algorithm into LoadBalancer; track active conns per request"
```

---

### Task 8: `internal/config` — YAML + CLI config

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VijayGohel/go-lb/internal/config"
)

func TestDefaults(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Server.Port != 3030 {
		t.Errorf("default port: want 3030, got %d", cfg.Server.Port)
	}
	if cfg.Pool.Algorithm != "round_robin" {
		t.Errorf("default algorithm: want round_robin, got %s", cfg.Pool.Algorithm)
	}
	if cfg.HealthCheck.Path != "/health" {
		t.Errorf("default health path: want /health, got %s", cfg.HealthCheck.Path)
	}
	if cfg.HealthCheck.Interval != 10*time.Second {
		t.Errorf("default interval: want 10s, got %s", cfg.HealthCheck.Interval)
	}
	if cfg.HealthCheck.Timeout != 2*time.Second {
		t.Errorf("default timeout: want 2s, got %s", cfg.HealthCheck.Timeout)
	}
	if cfg.HealthCheck.UnhealthyThreshold != 3 {
		t.Errorf("default unhealthy_threshold: want 3, got %d", cfg.HealthCheck.UnhealthyThreshold)
	}
	if cfg.HealthCheck.HealthyThreshold != 2 {
		t.Errorf("default healthy_threshold: want 2, got %d", cfg.HealthCheck.HealthyThreshold)
	}
	if cfg.Admin.Port != 9090 {
		t.Errorf("default admin port: want 9090, got %d", cfg.Admin.Port)
	}
}

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "golb-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestLoad_YAMLOnly(t *testing.T) {
	path := writeYAML(t, `
server:
  port: 4040
pool:
  algorithm: least_connections
  backends:
    - url: http://localhost:8081
      weight: 3
    - url: http://localhost:8082
      weight: 1
health_check:
  path: /ping
  interval: 5s
  timeout: 1s
  unhealthy_threshold: 5
  healthy_threshold: 3
admin:
  port: 8080
`)
	cfg, err := config.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 4040 {
		t.Errorf("port: want 4040, got %d", cfg.Server.Port)
	}
	if cfg.Pool.Algorithm != "least_connections" {
		t.Errorf("algorithm: want least_connections, got %s", cfg.Pool.Algorithm)
	}
	if len(cfg.Pool.Backends) != 2 {
		t.Fatalf("backends: want 2, got %d", len(cfg.Pool.Backends))
	}
	if cfg.Pool.Backends[0].URL != "http://localhost:8081" {
		t.Errorf("backend[0] url: want http://localhost:8081, got %s", cfg.Pool.Backends[0].URL)
	}
	if cfg.Pool.Backends[0].Weight != 3 {
		t.Errorf("backend[0] weight: want 3, got %d", cfg.Pool.Backends[0].Weight)
	}
	if cfg.HealthCheck.Interval != 5*time.Second {
		t.Errorf("interval: want 5s, got %s", cfg.HealthCheck.Interval)
	}
	if cfg.Admin.Port != 8080 {
		t.Errorf("admin port: want 8080, got %d", cfg.Admin.Port)
	}
}

func TestLoad_CLIOverridesYAML(t *testing.T) {
	path := writeYAML(t, `
server:
  port: 4040
pool:
  algorithm: least_connections
  backends:
    - url: http://localhost:8081
`)
	cfg, err := config.Load(path, []string{"--port=9999", "--algorithm=round_robin"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("CLI --port should override YAML: want 9999, got %d", cfg.Server.Port)
	}
	if cfg.Pool.Algorithm != "round_robin" {
		t.Errorf("CLI --algorithm should override YAML: want round_robin, got %s", cfg.Pool.Algorithm)
	}
}

func TestLoad_CLIBackendsOverrideYAML(t *testing.T) {
	path := writeYAML(t, `
pool:
  backends:
    - url: http://localhost:8081
`)
	cfg, err := config.Load(path, []string{"--backends=http://localhost:9091,http://localhost:9092"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Pool.Backends) != 2 {
		t.Fatalf("want 2 backends from CLI, got %d", len(cfg.Pool.Backends))
	}
	if cfg.Pool.Backends[0].URL != "http://localhost:9091" {
		t.Errorf("backend[0]: want http://localhost:9091, got %s", cfg.Pool.Backends[0].URL)
	}
}

func TestLoad_NoYAML_CLIOnly(t *testing.T) {
	cfg, err := config.Load("", []string{"--port=7070", "--backends=http://localhost:8081"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 7070 {
		t.Errorf("want port 7070, got %d", cfg.Server.Port)
	}
	if len(cfg.Pool.Backends) != 1 {
		t.Fatalf("want 1 backend, got %d", len(cfg.Pool.Backends))
	}
}

func TestLoad_MissingYAMLFile_ReturnsError(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.yaml"), nil)
	if err == nil {
		t.Fatal("expected error for missing YAML file")
	}
}

func TestLoad_UnsetCLIFlagsDoNotOverrideYAML(t *testing.T) {
	// Only --port is set; --algorithm must remain from YAML.
	path := writeYAML(t, `
pool:
  algorithm: least_connections
`)
	cfg, err := config.Load(path, []string{"--port=5050"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pool.Algorithm != "least_connections" {
		t.Errorf("unset CLI flag should not override YAML: got %s", cfg.Pool.Algorithm)
	}
}
```

- [ ] **Step 2: Run to verify fail**

```bash
go test ./internal/config/...
```
Expected: FAIL — package not found

- [ ] **Step 3: Create `internal/config/config.go`**

```go
package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the resolved, merged configuration.
type Config struct {
	Server      ServerConfig
	Pool        PoolConfig
	HealthCheck HealthCheckConfig
	Admin       AdminConfig
}

type ServerConfig struct {
	Port int
}

type PoolConfig struct {
	Algorithm string
	Backends  []BackendConfig
}

type BackendConfig struct {
	URL    string
	Weight int
}

type HealthCheckConfig struct {
	Path               string
	Interval           time.Duration
	Timeout            time.Duration
	UnhealthyThreshold int
	HealthyThreshold   int
}

type AdminConfig struct {
	Port int
}

// Defaults returns a Config pre-filled with production defaults.
func Defaults() Config {
	return Config{
		Server: ServerConfig{Port: 3030},
		Pool:   PoolConfig{Algorithm: "round_robin"},
		HealthCheck: HealthCheckConfig{
			Path:               "/health",
			Interval:           10 * time.Second,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
		Admin: AdminConfig{Port: 9090},
	}
}

// fileConfig mirrors Config for YAML unmarshalling using string durations.
type fileConfig struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Pool struct {
		Algorithm string `yaml:"algorithm"`
		Backends  []struct {
			URL    string `yaml:"url"`
			Weight int    `yaml:"weight"`
		} `yaml:"backends"`
	} `yaml:"pool"`
	HealthCheck struct {
		Path               string `yaml:"path"`
		Interval           string `yaml:"interval"`
		Timeout            string `yaml:"timeout"`
		UnhealthyThreshold int    `yaml:"unhealthy_threshold"`
		HealthyThreshold   int    `yaml:"healthy_threshold"`
	} `yaml:"health_check"`
	Admin struct {
		Port int `yaml:"port"`
	} `yaml:"admin"`
}

// Load reads the YAML file at path (if non-empty), then applies CLI overrides.
// CLI values only override when explicitly set on the command line.
func Load(path string, args []string) (Config, error) {
	cfg := Defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("reading config file: %w", err)
		}
		var fc fileConfig
		if err := yaml.Unmarshal(data, &fc); err != nil {
			return Config{}, fmt.Errorf("parsing config file: %w", err)
		}
		applyFileConfig(&cfg, &fc)
	}

	if err := applyCLI(&cfg, args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyFileConfig(cfg *Config, fc *fileConfig) {
	if fc.Server.Port != 0 {
		cfg.Server.Port = fc.Server.Port
	}
	if fc.Pool.Algorithm != "" {
		cfg.Pool.Algorithm = fc.Pool.Algorithm
	}
	if len(fc.Pool.Backends) > 0 {
		cfg.Pool.Backends = make([]BackendConfig, len(fc.Pool.Backends))
		for i, b := range fc.Pool.Backends {
			w := b.Weight
			if w <= 0 {
				w = 1
			}
			cfg.Pool.Backends[i] = BackendConfig{URL: b.URL, Weight: w}
		}
	}
	if fc.HealthCheck.Path != "" {
		cfg.HealthCheck.Path = fc.HealthCheck.Path
	}
	if fc.HealthCheck.Interval != "" {
		if d, err := time.ParseDuration(fc.HealthCheck.Interval); err == nil {
			cfg.HealthCheck.Interval = d
		}
	}
	if fc.HealthCheck.Timeout != "" {
		if d, err := time.ParseDuration(fc.HealthCheck.Timeout); err == nil {
			cfg.HealthCheck.Timeout = d
		}
	}
	if fc.HealthCheck.UnhealthyThreshold != 0 {
		cfg.HealthCheck.UnhealthyThreshold = fc.HealthCheck.UnhealthyThreshold
	}
	if fc.HealthCheck.HealthyThreshold != 0 {
		cfg.HealthCheck.HealthyThreshold = fc.HealthCheck.HealthyThreshold
	}
	if fc.Admin.Port != 0 {
		cfg.Admin.Port = fc.Admin.Port
	}
}

func applyCLI(cfg *Config, args []string) error {
	fs := flag.NewFlagSet("golb", flag.ContinueOnError)

	var (
		port            int
		backendsRaw     string
		algorithm       string
		healthPath      string
		healthInterval  time.Duration
		healthTimeout   time.Duration
		adminPort       int
	)

	fs.IntVar(&port, "port", 0, "Port to listen on")
	fs.StringVar(&backendsRaw, "backends", "", "Comma-separated backend URLs (weight=1)")
	fs.StringVar(&algorithm, "algorithm", "", "Algorithm: round_robin|least_connections|weighted_round_robin")
	fs.StringVar(&healthPath, "health-path", "", "Health check path")
	fs.DurationVar(&healthInterval, "health-interval", 0, "Health check interval")
	fs.DurationVar(&healthTimeout, "health-timeout", 0, "Health check timeout")
	fs.IntVar(&adminPort, "admin-port", 0, "Admin server port (0=disabled)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing CLI flags: %w", err)
	}

	// Only override when the flag was explicitly provided on the command line.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			cfg.Server.Port = port
		case "backends":
			cfg.Pool.Backends = nil
			for _, raw := range strings.Split(backendsRaw, ",") {
				if raw = strings.TrimSpace(raw); raw != "" {
					cfg.Pool.Backends = append(cfg.Pool.Backends, BackendConfig{URL: raw, Weight: 1})
				}
			}
		case "algorithm":
			cfg.Pool.Algorithm = algorithm
		case "health-path":
			cfg.HealthCheck.Path = healthPath
		case "health-interval":
			cfg.HealthCheck.Interval = healthInterval
		case "health-timeout":
			cfg.HealthCheck.Timeout = healthTimeout
		case "admin-port":
			cfg.Admin.Port = adminPort
		}
	})
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/...
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): YAML file + CLI flag merge with flag.Visit override detection"
```

---

### Task 9: `internal/health` — threshold state machine

**Files:**
- Modify: `internal/health/health.go`
- Modify: `internal/health/health_test.go`

- [ ] **Step 1: Add failing threshold tests to `internal/health/health_test.go`**

Append to existing test file:

```go
func TestHealthChecker_UnhealthyThreshold_MarksDeadAfterNFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	b := makeBackend(srv.URL, true)
	p.AddBackend(b)

	// unhealthyThreshold=3: backend must stay alive for first 2 failures
	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithUnhealthyThreshold(3),
		health.WithHealthyThreshold(2),
	)

	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 1st failure (threshold=3): backend should still be alive")
	}

	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 2nd failure (threshold=3): backend should still be alive")
	}

	hc.CheckBackend(context.Background(), b)
	if b.IsAlive() {
		t.Fatal("after 3rd failure (threshold=3): backend should be dead")
	}
}

func TestHealthChecker_HealthyThreshold_MarksAliveAfterNSuccesses(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	b := makeBackend(srv.URL, false) // start dead
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithUnhealthyThreshold(3),
		health.WithHealthyThreshold(2),
	)

	healthy.Store(true)

	hc.CheckBackend(context.Background(), b)
	if b.IsAlive() {
		t.Fatal("after 1st success (healthyThreshold=2): backend should still be dead")
	}

	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 2nd success (healthyThreshold=2): backend should be alive")
	}
}

func TestHealthChecker_DirectionChange_ResetsCounter(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	p := &pool.ServerPool{}
	b := makeBackend(srv.URL, true) // start alive
	p.AddBackend(b)

	hc := health.NewHealthChecker(p, "/health", time.Second, time.Second,
		health.WithUnhealthyThreshold(3),
		health.WithHealthyThreshold(2),
	)

	// 2 failures — not yet at threshold
	hc.CheckBackend(context.Background(), b)
	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after 2 failures (threshold=3): backend should still be alive")
	}

	// 1 success — resets counter
	healthy.Store(true)
	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("after direction change (success): backend should still be alive")
	}

	// 2 more failures (fresh count) — not at threshold yet
	healthy.Store(false)
	hc.CheckBackend(context.Background(), b)
	hc.CheckBackend(context.Background(), b)
	if !b.IsAlive() {
		t.Fatal("counter was reset; 2 failures with threshold=3 should not kill backend")
	}
}
```

- [ ] **Step 2: Run to verify fail**

```bash
go test ./internal/health/...
```
Expected: FAIL — `health.WithUnhealthyThreshold undefined`

- [ ] **Step 3: Rewrite `internal/health/health.go`**

```go
package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/pool"
)

// HealthChecker probes backend health on a configurable interval.
// Consecutive counters track direction changes to implement unhealthy/healthy thresholds.
type HealthChecker struct {
	pool               *pool.ServerPool
	path               string
	interval           time.Duration
	timeout            time.Duration
	unhealthyThreshold int
	healthyThreshold   int
	client             *http.Client
	mu                 sync.Mutex
	consecutive        map[string]int // +ve = consecutive successes, -ve = consecutive failures
}

// Option configures a HealthChecker.
type Option func(*HealthChecker)

// WithUnhealthyThreshold sets the number of consecutive failures before marking a backend dead.
func WithUnhealthyThreshold(n int) Option {
	return func(hc *HealthChecker) { hc.unhealthyThreshold = n }
}

// WithHealthyThreshold sets the number of consecutive successes before marking a dead backend alive.
func WithHealthyThreshold(n int) Option {
	return func(hc *HealthChecker) { hc.healthyThreshold = n }
}

// NewHealthChecker creates a HealthChecker with defaults: interval=10s, timeout=2s,
// unhealthyThreshold=3, healthyThreshold=2. Callers may override via Option functions.
func NewHealthChecker(p *pool.ServerPool, path string, interval, timeout time.Duration, opts ...Option) *HealthChecker {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	hc := &HealthChecker{
		pool:               p,
		path:               path,
		interval:           interval,
		timeout:            timeout,
		unhealthyThreshold: 3,
		healthyThreshold:   2,
		client:             &http.Client{Timeout: timeout},
		consecutive:        make(map[string]int),
	}
	for _, o := range opts {
		o(hc)
	}
	return hc
}

// CheckBackend performs a single HTTP health probe and updates alive state via thresholds.
func (hc *HealthChecker) CheckBackend(ctx context.Context, b *backend.Backend) {
	target := b.URL.String() + hc.path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		hc.recordFailure(b)
		return
	}
	resp, err := hc.client.Do(req)
	if err != nil {
		hc.recordFailure(b)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		hc.recordFailure(b)
		return
	}
	hc.recordSuccess(b)
}

func (hc *HealthChecker) recordSuccess(b *backend.Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	key := b.URL.String()
	if hc.consecutive[key] < 0 {
		hc.consecutive[key] = 0 // direction changed
	}
	hc.consecutive[key]++
	if hc.consecutive[key] >= hc.healthyThreshold && !b.IsAlive() {
		b.SetAlive(true)
		hc.consecutive[key] = 0
		slog.Info("backend_up", "backend", key)
	}
}

func (hc *HealthChecker) recordFailure(b *backend.Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	key := b.URL.String()
	if hc.consecutive[key] > 0 {
		hc.consecutive[key] = 0 // direction changed
	}
	hc.consecutive[key]--
	if -hc.consecutive[key] >= hc.unhealthyThreshold && b.IsAlive() {
		b.SetAlive(false)
		hc.consecutive[key] = 0
		slog.Warn("backend_down", "backend", key, "error", "unhealthy threshold reached")
	}
}

// Start runs health checks on all pool backends every interval until ctx is cancelled.
func (hc *HealthChecker) Start(ctx context.Context) {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var wg sync.WaitGroup
			for _, b := range hc.pool.Backends() {
				wg.Add(1)
				go func(b *backend.Backend) {
					defer wg.Done()
					hc.CheckBackend(ctx, b)
				}(b)
			}
			wg.Wait()
		case <-ctx.Done():
			return
		}
	}
}
```

- [ ] **Step 4: Update existing health tests — constructor signature changed**

The existing tests call `health.NewHealthChecker(p, "/health", time.Second, time.Second)` which still compiles since `opts ...Option` is variadic. Verify:

```bash
go test ./internal/health/...
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/health/
git commit -m "feat(health): add consecutive-counter threshold state machine (unhealthy/healthy)"
```

---

### Task 10: `internal/admin` — REST API server

**Files:**
- Create: `internal/admin/admin.go`
- Create: `internal/admin/admin_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/admin/admin_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify fail**

```bash
go test ./internal/admin/...
```
Expected: FAIL — package not found

- [ ] **Step 3: Create `internal/admin/admin.go`**

```go
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

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
func (s *Server) Start(ctx context.Context, addr string) *http.Server {
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
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
	w8 := req.Weight
	if w8 <= 0 {
		w8 = 1
	}
	b := &backend.Backend{URL: u, Weight: w8}
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
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/admin/...
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/admin/
git commit -m "feat(admin): REST API server with list/add/remove/enable/disable endpoints"
```

---

### Task 11: Update `cmd/golb/main.go` — wire everything + dual-server shutdown

**Files:**
- Modify: `cmd/golb/main.go`

- [ ] **Step 1: Rewrite `cmd/golb/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VijayGohel/go-lb/internal/admin"
	"github.com/VijayGohel/go-lb/internal/algo"
	"github.com/VijayGohel/go-lb/internal/backend"
	"github.com/VijayGohel/go-lb/internal/config"
	"github.com/VijayGohel/go-lb/internal/health"
	"github.com/VijayGohel/go-lb/internal/logger"
	"github.com/VijayGohel/go-lb/internal/pool"
	"github.com/VijayGohel/go-lb/internal/proxy"
)

func main() {
	// Determine config file path from first arg if it looks like a --config= flag.
	cfgPath := ""
	args := os.Args[1:]
	for i, a := range args {
		if len(a) > 9 && a[:9] == "--config=" {
			cfgPath = a[9:]
			args = append(args[:i], args[i+1:]...)
			break
		}
	}

	cfg, err := config.Load(cfgPath, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	logger.Init()

	if len(cfg.Pool.Backends) == 0 {
		slog.Error("no backends configured — use --backends or a config file")
		os.Exit(1)
	}

	a, err := algo.New(cfg.Pool.Algorithm)
	if err != nil {
		slog.Error("invalid algorithm", "error", err)
		os.Exit(1)
	}

	p := &pool.ServerPool{}
	lb := proxy.New(p, a)

	for _, bc := range cfg.Pool.Backends {
		u, err := url.Parse(bc.URL)
		if err != nil {
			slog.Error("invalid backend URL", "url", bc.URL, "error", err)
			os.Exit(1)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			slog.Error("backend URL must use http or https scheme", "url", bc.URL)
			os.Exit(1)
		}
		if u.Host == "" {
			slog.Error("backend URL must include a host", "url", bc.URL)
			os.Exit(1)
		}
		b := &backend.Backend{URL: u, Weight: bc.Weight}
		b.SetAlive(true)
		lb.SetupProxy(b)
		p.AddBackend(b)
		slog.Info("registered backend", "url", u.String(), "weight", bc.Weight)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hc := health.NewHealthChecker(p,
		cfg.HealthCheck.Path,
		cfg.HealthCheck.Interval,
		cfg.HealthCheck.Timeout,
		health.WithUnhealthyThreshold(cfg.HealthCheck.UnhealthyThreshold),
		health.WithHealthyThreshold(cfg.HealthCheck.HealthyThreshold),
	)
	go hc.Start(ctx)

	mainSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: lb,
	}

	var adminSrv *http.Server
	if cfg.Admin.Port != 0 {
		adminSrv = admin.New(p, lb).Start(ctx, fmt.Sprintf(":%d", cfg.Admin.Port))
		go func() {
			slog.Info("admin server started", "port", cfg.Admin.Port)
			if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("admin server error", "error", err)
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		slog.Info("shutting down gracefully")
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		if adminSrv != nil {
			if err := adminSrv.Shutdown(shutCtx); err != nil {
				slog.Error("admin shutdown error", "error", err)
			}
		}
		if err := mainSrv.Shutdown(shutCtx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
	}()

	slog.Info("golb started",
		"port", cfg.Server.Port,
		"algorithm", cfg.Pool.Algorithm,
		"backends", len(p.Backends()),
	)
	if err := mainSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Build to verify it compiles**

```bash
go build ./cmd/golb/
```
Expected: exits 0, binary `golb` created

- [ ] **Step 3: Run full test suite**

```bash
go test ./...
```
Expected: all PASS

- [ ] **Step 4: Run linter**

```bash
make lint
```
Expected: no issues

- [ ] **Step 5: Commit**

```bash
git add cmd/golb/main.go
git commit -m "feat(main): wire config/algo/admin + dual-server graceful shutdown"
```

---

### Task 12: Integration smoke test + tag v2.0.0

**Files:**
- No new files — manual smoke test only

- [ ] **Step 1: Run full test suite one final time**

```bash
go test ./...
```
Expected: all PASS

- [ ] **Step 2: Build release binary**

```bash
make build
```
Expected: `golb` binary produced

- [ ] **Step 3: Smoke test with YAML config**

Create a temporary config file:

```bash
cat > /tmp/golb-test.yaml << 'EOF'
server:
  port: 3030
pool:
  algorithm: round_robin
  backends:
    - url: http://localhost:8081
      weight: 1
health_check:
  path: /health
  interval: 10s
  timeout: 2s
  unhealthy_threshold: 3
  healthy_threshold: 2
admin:
  port: 9090
EOF
```

Start the binary (it will fail to connect to backends but must start):

```bash
./golb --config=/tmp/golb-test.yaml &
sleep 1
curl -s http://localhost:9090/admin/backends | jq .
kill %1
```
Expected: JSON array with one backend entry.

- [ ] **Step 4: Verify `--help` shows all V1 flags**

```bash
./golb --help 2>&1 | grep -E "config|port|backend|algo|health|admin"
```

- [ ] **Step 5: Raise a PR, get it merged, then tag**

```bash
git push -u origin feat/golb-v1
# Open PR via GitHub UI or gh CLI, merge it, then:
git checkout main
git pull
git tag -a v2.0.0 -m "V1: YAML config, pluggable algorithms, admin API, health thresholds"
git push origin v2.0.0
```

- [ ] **Step 6: Clean up**

```bash
rm -f golb /tmp/golb-test.yaml
```
