// Package intercept pauses an in-flight request until the operator
// posts a Decision (forward — possibly with edits — or drop).
package intercept

import (
	"context"
	"log"
	"sync"
	"sync/atomic"

	"github.com/scribelia-anthony/prompt-cleaner/internal/persist"
)

type Action string

const (
	ActionForward Action = "forward"
	ActionDrop    Action = "drop"
)

type Decision struct {
	Action   Action
	URL      string
	Headers  map[string][]string
	Body     string
	Modified bool
}

type Interceptor struct {
	enabled atomic.Bool

	mu      sync.Mutex
	pending map[string]chan Decision
	path    string
}

type persisted struct {
	Enabled bool `json:"enabled"`
}

func New() *Interceptor {
	return &Interceptor{pending: make(map[string]chan Decision)}
}

func (i *Interceptor) Enabled() bool { return i.enabled.Load() }

func (i *Interceptor) Set(v bool) {
	i.enabled.Store(v)
	i.save()
}

// SetPath wires a JSON file to back the intercept toggle across restarts.
// Empty path disables persistence. Call Load() afterwards to pick up any
// existing on-disk state.
func (i *Interceptor) SetPath(p string) {
	i.mu.Lock()
	i.path = p
	i.mu.Unlock()
}

func (i *Interceptor) Load() {
	i.mu.Lock()
	path := i.path
	i.mu.Unlock()
	if path == "" {
		return
	}
	var p persisted
	if err := persist.ReadJSON(path, &p); err != nil {
		return
	}
	i.enabled.Store(p.Enabled)
}

func (i *Interceptor) save() {
	i.mu.Lock()
	path := i.path
	i.mu.Unlock()
	if path == "" {
		return
	}
	if err := persist.WriteJSON(path, persisted{Enabled: i.enabled.Load()}); err != nil {
		log.Printf("[intercept] persist to %s failed: %v", path, err)
	}
}

// Await registers a pending intercepted request and blocks until a decision
// is posted (via Decide) or the context is cancelled.
func (i *Interceptor) Await(ctx context.Context, id string) (Decision, bool) {
	ch := make(chan Decision, 1)
	i.mu.Lock()
	i.pending[id] = ch
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		delete(i.pending, id)
		i.mu.Unlock()
	}()
	select {
	case d := <-ch:
		return d, true
	case <-ctx.Done():
		return Decision{}, false
	}
}

// Decide delivers a decision for a pending intercepted request. Returns
// false if no such pending request exists.
func (i *Interceptor) Decide(id string, d Decision) bool {
	i.mu.Lock()
	ch, ok := i.pending[id]
	i.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- d:
		return true
	default:
		return false
	}
}

func (i *Interceptor) Pending() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]string, 0, len(i.pending))
	for id := range i.pending {
		out = append(out, id)
	}
	return out
}
