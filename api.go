package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type API struct {
	store       *Store
	rules       *Rules
	interceptor *Interceptor
	proxy       *Proxy
}

func NewAPI(s *Store, r *Rules, i *Interceptor, p *Proxy) *API {
	return &API{store: s, rules: r, interceptor: i, proxy: p}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/captures", a.handleCaptures)
	mux.HandleFunc("/admin/captures/", a.handleCaptureItem)
	mux.HandleFunc("/admin/intercept", a.handleIntercept)
	mux.HandleFunc("/admin/rules", a.handleRules)
	mux.HandleFunc("/admin/clear", a.handleClear)
	mux.HandleFunc("/admin/state", a.handleState)
	mux.HandleFunc("/events", a.handleEvents)
}

func (a *API) handleCaptures(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	setJSON(w)
	_, _ = w.Write(a.store.SnapshotAll())
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
			http.Error(w, "method not allowed", 405)
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
			http.Error(w, "method not allowed", 405)
			return
		}
		a.handleForward(w, r, id)
	case "drop":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		a.handleDrop(w, r, id)
	case "replay":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
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

	modified := false
	if req.URL != orig.Request.URL {
		modified = true
	}
	if req.Body != orig.Request.Body {
		modified = true
	}
	if !sameHeaders(req.Headers, orig.Request.Headers) {
		modified = true
	}

	if !a.interceptor.Decide(id, Decision{
		Action:   ActionForward,
		URL:      req.URL,
		Headers:  req.Headers,
		Body:     req.Body,
		Modified: modified,
	}) {
		http.Error(w, "not intercepted", 409)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleDrop(w http.ResponseWriter, r *http.Request, id string) {
	if !a.interceptor.Decide(id, Decision{Action: ActionDrop}) {
		http.Error(w, "not intercepted", 409)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type interceptReq struct {
	Enabled bool `json:"enabled"`
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
		a.store.Broadcast(Event{Type: "intercept_toggled", Payload: payload})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (a *API) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		setJSON(w)
		_ = json.NewEncoder(w).Encode(a.rules.List())
	case http.MethodPut:
		var rules []*Rule
		if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := a.rules.Replace(rules); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		listed := a.rules.List()
		setJSON(w)
		_ = json.NewEncoder(w).Encode(listed)
		payload, _ := json.Marshal(listed)
		a.store.Broadcast(Event{Type: "rules_updated", Payload: payload})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (a *API) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	a.store.Clear()
	w.WriteHeader(http.StatusNoContent)
	a.store.Broadcast(Event{Type: "captures_cleared"})
}

func (a *API) handleState(w http.ResponseWriter, r *http.Request) {
	setJSON(w)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"intercept":  a.interceptor.Enabled(),
		"pending":    a.interceptor.Pending(),
		"rules":      a.rules.List(),
		"upstream":   a.proxy.upstream.String(),
		"proxy_addr": "",
	})
}

func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := a.store.Subscribe()
	defer a.store.Unsubscribe(ch)

	// initial snapshot of state + captures
	state := map[string]any{
		"intercept": a.interceptor.Enabled(),
		"pending":   a.interceptor.Pending(),
		"rules":     a.rules.List(),
		"captures":  json.RawMessage(a.store.SnapshotAll()),
		"upstream":  a.proxy.upstream.String(),
	}
	statePayload, _ := json.Marshal(state)
	fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", statePayload)
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev, more := <-ch:
			if !more {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
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

	// Run the replay in its own context so the HTTP response to the UI can return
	// immediately while the replay completes in the background.
	bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	hreq = hreq.WithContext(context.WithValue(bgCtx, replayOriginKey{}, originalID))

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
