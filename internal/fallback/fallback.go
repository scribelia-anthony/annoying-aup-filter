// Package fallback holds the user-configurable AUP-refusal fallback
// policy. When enabled, the proxy detects an SSE `stop_reason: "refusal"`
// arriving before any content is generated, swallows that response, and
// reissues the same request to upstream with `model` replaced by the
// configured fallback model. The retry runs at most once per request:
// if the fallback also refuses, that refusal reaches the client.
package fallback

import (
	"log"
	"sync"
	"sync/atomic"

	"github.com/scribelia-anthony/annoying-aup-filter/internal/persist"
)

const DefaultModel = "claude-opus-4-6"

type Fallback struct {
	enabled atomic.Bool
	mu      sync.Mutex
	model   string
	path    string
}

type persisted struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
}

func New() *Fallback {
	return &Fallback{model: DefaultModel}
}

func (f *Fallback) Enabled() bool { return f.enabled.Load() }

func (f *Fallback) Set(v bool) {
	f.enabled.Store(v)
	f.save()
}

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
	f.save()
}

// SetPath wires a JSON file to back the fallback toggle/model across
// restarts. Empty path disables persistence. Call Load() afterwards to
// pick up any existing on-disk state.
func (f *Fallback) SetPath(p string) {
	f.mu.Lock()
	f.path = p
	f.mu.Unlock()
}

// Load reads the configured path (if any) and applies it.
func (f *Fallback) Load() {
	f.mu.Lock()
	path := f.path
	f.mu.Unlock()
	if path == "" {
		return
	}
	var p persisted
	if err := persist.ReadJSON(path, &p); err != nil {
		return // missing or unreadable — keep defaults
	}
	f.enabled.Store(p.Enabled)
	if p.Model != "" {
		f.mu.Lock()
		f.model = p.Model
		f.mu.Unlock()
	}
}

func (f *Fallback) save() {
	f.mu.Lock()
	path := f.path
	model := f.model
	f.mu.Unlock()
	if path == "" {
		return
	}
	if err := persist.WriteJSON(path, persisted{Enabled: f.enabled.Load(), Model: model}); err != nil {
		log.Printf("[fallback] persist to %s failed: %v", path, err)
	}
}
