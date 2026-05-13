package main

import (
	"sync"
	"sync/atomic"
)

// Fallback holds the user-configurable AUP-refusal fallback policy.
//
// When enabled, the proxy detects an SSE `stop_reason: "refusal"` arriving
// before any content was generated, swallows that response, and reissues
// the same request to upstream with `model` replaced by `FallbackModel`.
//
// Only triggers once per request (no loop): if the fallback also refuses,
// that refusal is forwarded to the client.
type Fallback struct {
	enabled atomic.Bool
	mu      sync.Mutex
	model   string
}

func NewFallback() *Fallback {
	f := &Fallback{model: "claude-sonnet-4-6"}
	return f
}

func (f *Fallback) Enabled() bool { return f.enabled.Load() }
func (f *Fallback) Set(v bool)    { f.enabled.Store(v) }

func (f *Fallback) Model() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.model
}

func (f *Fallback) SetModel(m string) {
	if m == "" {
		return
	}
	f.mu.Lock()
	f.model = m
	f.mu.Unlock()
}
