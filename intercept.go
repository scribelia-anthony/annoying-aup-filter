package main

import (
	"context"
	"sync"
	"sync/atomic"
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
}

func NewInterceptor() *Interceptor {
	return &Interceptor{pending: make(map[string]chan Decision)}
}

func (i *Interceptor) Enabled() bool { return i.enabled.Load() }
func (i *Interceptor) Set(v bool)    { i.enabled.Store(v) }

// Await registers a pending intercepted request and blocks until a decision is
// posted (via Decide) or the context is cancelled.
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

// Decide delivers a decision for a pending intercepted request.
// Returns false if no such pending request exists.
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
