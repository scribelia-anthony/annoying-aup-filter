// Package fallback holds the user-configurable AUP-refusal fallback
// policy. When enabled, the proxy detects an SSE `stop_reason: "refusal"`
// arriving before any content is generated, swallows that response, and
// reissues the same request to upstream with `model` replaced by the
// configured fallback model. The retry runs at most once per request:
// if the fallback also refuses, that refusal reaches the client.
package fallback

import (
	"sync"
	"sync/atomic"
)

const DefaultModel = "claude-opus-4-6"

type Fallback struct {
	enabled atomic.Bool
	mu      sync.Mutex
	model   string
}

func New() *Fallback {
	return &Fallback{model: DefaultModel}
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
