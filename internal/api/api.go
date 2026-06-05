// Package api wires the admin REST endpoints (capture inspection, rule
// CRUD, intercept/fallback toggles) and the `/events` SSE stream that
// powers the live web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/scribelia-anthony/annoying-aup-filter/internal/fallback"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/intercept"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/proxy"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/rules"
	"github.com/scribelia-anthony/annoying-aup-filter/internal/store"
)

type API struct {
	store       *store.Store
	rules       *rules.Rules
	interceptor *intercept.Interceptor
	fallback    *fallback.Fallback
	proxy       *proxy.Proxy
	ProxyAddr   string
}

func New(s *store.Store, r *rules.Rules, i *intercept.Interceptor, f *fallback.Fallback, p *proxy.Proxy) *API {
	return &API{store: s, rules: r, interceptor: i, fallback: f, proxy: p}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/captures", a.handleCaptures)
	mux.HandleFunc("/admin/captures/", a.handleCaptureItem)
	mux.HandleFunc("/admin/intercept", a.handleIntercept)
	mux.HandleFunc("/admin/fallback", a.handleFallback)
	mux.HandleFunc("/admin/rules", a.handleRules)
	mux.HandleFunc("/admin/clear", a.handleClear)
	mux.HandleFunc("/admin/state", a.handleState)
	mux.HandleFunc("/events", a.handleEvents)
}

func (a *API) handleCaptures(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	setJSON(w)
	_, _ = w.Write(a.store.SnapshotAllSummaries())
}

func (a *API) handleCaptureItem(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/captures/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if id == "" {
		http.Error(w, "missing id", 400)
		return
	}

	switch action {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snap, ok := a.store.Snapshot(id)
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		setJSON(w)
		_, _ = w.Write(snap)
	case "forward":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleForward(w, r, id)
	case "drop":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleDrop(w, r, id)
	case "replay":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleReplay(w, r, id)
	default:
		http.Error(w, "not found", 404)
	}
}

type forwardReq struct {
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

func (a *API) handleForward(w http.ResponseWriter, r *http.Request, id string) {
	var req forwardReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	orig, ok := a.store.Capture(id)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}

	modified := req.URL != orig.Request.URL ||
		req.Body != orig.Request.Body ||
		!sameHeaders(req.Headers, orig.Request.Headers)

	if !a.interceptor.Decide(id, intercept.Decision{
		Action:   intercept.ActionForward,
		URL:      req.URL,
		Headers:  req.Headers,
		Body:     req.Body,
		Modified: modified,
	}) {
		http.Error(w, "not intercepted", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDrop(w http.ResponseWriter, _ *http.Request, id string) {
	if !a.interceptor.Decide(id, intercept.Decision{Action: intercept.ActionDrop}) {
		http.Error(w, "not intercepted", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type interceptReq struct {
	Enabled bool `json:"enabled"`
}

type fallbackReq struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
}

func (a *API) handleFallback(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		setJSON(w)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled": a.fallback.Enabled(),
			"model":   a.fallback.Model(),
		})
	case http.MethodPost:
		var req fallbackReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.fallback.Set(req.Enabled)
		if req.Model != "" {
			a.fallback.SetModel(req.Model)
		}
		state := map[string]any{
			"enabled": a.fallback.Enabled(),
			"model":   a.fallback.Model(),
		}
		setJSON(w)
		_ = json.NewEncoder(w).Encode(state)
		payload, _ := json.Marshal(state)
		a.store.Broadcast(store.Event{Type: "fallback_toggled", Payload: payload})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleIntercept(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		setJSON(w)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled": a.interceptor.Enabled(),
			"pending": a.interceptor.Pending(),
		})
	case http.MethodPost:
		var req interceptReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.interceptor.Set(req.Enabled)
		setJSON(w)
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": a.interceptor.Enabled()})
		payload, _ := json.Marshal(map[string]any{"enabled": req.Enabled})
		a.store.Broadcast(store.Event{Type: "intercept_toggled", Payload: payload})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		setJSON(w)
		_ = json.NewEncoder(w).Encode(a.rules.List())
	case http.MethodPut:
		var rs []*rules.Rule
		if err := json.NewDecoder(r.Body).Decode(&rs); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := a.rules.Replace(rs); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		listed := a.rules.List()
		setJSON(w)
		_ = json.NewEncoder(w).Encode(listed)
		payload, _ := json.Marshal(listed)
		a.store.Broadcast(store.Event{Type: "rules_updated", Payload: payload})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.store.Clear()
	w.WriteHeader(http.StatusNoContent)
	a.store.Broadcast(store.Event{Type: "captures_cleared"})
}

func (a *API) handleState(w http.ResponseWriter, r *http.Request) {
	setJSON(w)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"intercept":      a.interceptor.Enabled(),
		"pending":        a.interceptor.Pending(),
		"rules":          a.rules.List(),
		"upstream":       a.proxy.Upstream(),
		"proxy_addr":     a.ProxyAddr,
		"fallback":       a.fallback.Enabled(),
		"fallback_model": a.fallback.Model(),
	})
}

// sseWriteTimeout bounds every individual SSE write so a half-open
// client (Chrome tab killed, network suspended, …) cannot wedge a
// goroutine on Write forever. Picked larger than a normal flush latency
// but small enough that a dead connection drops within seconds.
const sseWriteTimeout = 5 * time.Second

func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)

	ch := a.store.Subscribe()
	defer a.store.Unsubscribe(ch)

	state := map[string]any{
		"intercept":      a.interceptor.Enabled(),
		"pending":        a.interceptor.Pending(),
		"rules":          a.rules.List(),
		"captures":       a.store.SnapshotAllSummaries(),
		"upstream":       a.proxy.Upstream(),
		"proxy_addr":     a.ProxyAddr,
		"fallback":       a.fallback.Enabled(),
		"fallback_model": a.fallback.Model(),
	}
	statePayload, _ := json.Marshal(state)
	if err := writeSSE(rc, w, flusher, []byte("event: snapshot\ndata: "), statePayload, []byte("\n\n")); err != nil {
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev, more := <-ch:
			if !more {
				return
			}
			data, _ := json.Marshal(ev)
			if err := writeSSE(rc, w, flusher, []byte("data: "), data, []byte("\n\n")); err != nil {
				return
			}
		case <-ticker.C:
			if err := writeSSE(rc, w, flusher, []byte(": heartbeat\n\n")); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// writeSSE serialises one SSE record under a per-write deadline so a
// dead client trips an error instead of pinning the goroutine. Any
// write or flush error returns immediately — the caller drops the
// connection and the surrounding defer cleans up the subscriber.
func writeSSE(rc *http.ResponseController, w http.ResponseWriter, flusher http.Flusher, parts ...[]byte) error {
	if err := rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	for _, p := range parts {
		if _, err := w.Write(p); err != nil {
			return err
		}
	}
	flusher.Flush()
	return nil
}

type replayReq struct {
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
	Method  string              `json:"method"`
}

func (a *API) handleReplay(w http.ResponseWriter, r *http.Request, originalID string) {
	var req replayReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	orig, ok := a.store.Capture(originalID)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}

	method := req.Method
	if method == "" {
		method = orig.Request.Method
	}
	urlStr := req.URL
	if urlStr == "" {
		urlStr = orig.Request.URL
	}
	headers := req.Headers
	if headers == nil {
		headers = orig.Request.Headers
	}
	body := req.Body
	if body == "" {
		body = orig.Request.Body
	}

	hreq, err := http.NewRequest(method, urlStr, strings.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	for k, vs := range headers {
		for _, v := range vs {
			hreq.Header.Add(k, v)
		}
	}

	// Run the replay in its own context so the HTTP response to the UI
	// returns immediately while the replay completes in the background.
	bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	hreq = hreq.WithContext(proxy.WithReplayOrigin(bgCtx, originalID))

	rw := &discardResponseWriter{header: http.Header{}}
	go func() {
		defer cancel()
		a.proxy.ServeHTTP(rw, hreq)
	}()

	setJSON(w)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "replay queued"})
}

type discardResponseWriter struct {
	header http.Header
	status int
}

func (d *discardResponseWriter) Header() http.Header         { return d.header }
func (d *discardResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardResponseWriter) WriteHeader(s int)           { d.status = s }
func (d *discardResponseWriter) Flush()                      {}

func setJSON(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json") }

func sameHeaders(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, vs := range a {
		bvs, ok := b[k]
		if !ok || len(bvs) != len(vs) {
			return false
		}
		for i := range vs {
			if vs[i] != bvs[i] {
				return false
			}
		}
	}
	return true
}
