package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Proxy struct {
	upstream    *url.URL
	store       *Store
	rules       *Rules
	interceptor *Interceptor
	fallback    *Fallback
	client      *http.Client
}

type replayOriginKey struct{}
type fallbackAttemptKey struct{}
type fallbackOriginKey struct{}

func NewProxy(upstream string, store *Store, rules *Rules, interceptor *Interceptor, fallback *Fallback) (*Proxy, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("upstream must include scheme and host (got %q)", upstream)
	}
	return &Proxy{
		upstream:    u,
		store:       store,
		rules:       rules,
		interceptor: interceptor,
		fallback:    fallback,
		client: &http.Client{
			Timeout: 0, // streaming responses can run for minutes
		},
	}, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	capID := newID()
	now := time.Now()
	initial := &Capture{
		ID:        capID,
		CreatedAt: now,
		StartedAt: now,
		Status:    StatusPending,
		Request: CapturedRequest{
			Method:  r.Method,
			URL:     r.URL.RequestURI(),
			Path:    r.URL.Path,
			Headers: cloneHeaders(r.Header),
			Body:    string(body),
		},
	}
	if v, ok := r.Context().Value(replayOriginKey{}).(string); ok {
		initial.ReplayOf = v
	}
	if v, ok := r.Context().Value(fallbackOriginKey{}).(string); ok {
		initial.FallbackOf = v
	}
	p.store.Add(initial)
	if snap, ok := p.store.Snapshot(capID); ok {
		p.store.Broadcast(Event{Type: "capture_started", ID: capID, Payload: snap})
	}

	urlStr := initial.Request.URL
	headers := cloneHeaders(r.Header)
	bodyCopy := append([]byte(nil), body...)
	if p.rules.ApplyRequest(&urlStr, headers, &bodyCopy) {
		snap, _ := p.store.Update(capID, func(c *Capture) {
			c.Modified = true
			c.Request.URL = urlStr
			c.Request.Path = pathOf(urlStr)
			c.Request.Headers = headers
			c.Request.Body = string(bodyCopy)
		})
		p.store.Broadcast(Event{Type: "capture_updated", ID: capID, Payload: snap})
	}

	if p.interceptor.Enabled() {
		snap, _ := p.store.Update(capID, func(c *Capture) { c.Status = StatusIntercepted })
		p.store.Broadcast(Event{Type: "capture_intercepted", ID: capID, Payload: snap})

		decision, ok := p.interceptor.Await(r.Context(), capID)
		if !ok {
			snap, _ := p.store.Update(capID, func(c *Capture) {
				c.Status = StatusErrored
				c.Error = "client cancelled while intercepted"
				end := time.Now()
				c.EndedAt = &end
			})
			p.store.Broadcast(Event{Type: "capture_errored", ID: capID, Payload: snap})
			http.Error(w, "intercept cancelled", 499)
			return
		}
		switch decision.Action {
		case ActionDrop:
			snap, _ := p.store.Update(capID, func(c *Capture) {
				c.Status = StatusDropped
				end := time.Now()
				c.EndedAt = &end
			})
			p.store.Broadcast(Event{Type: "capture_updated", ID: capID, Payload: snap})
			http.Error(w, "request dropped by interceptor", http.StatusForbidden)
			return
		case ActionForward:
			if decision.URL != "" {
				urlStr = decision.URL
			}
			if decision.Headers != nil {
				headers = decision.Headers
			}
			bodyCopy = []byte(decision.Body)
			snap, _ := p.store.Update(capID, func(c *Capture) {
				c.Request.URL = urlStr
				c.Request.Path = pathOf(urlStr)
				c.Request.Headers = headers
				c.Request.Body = string(bodyCopy)
				if decision.Modified {
					c.Modified = true
				}
			})
			p.store.Broadcast(Event{Type: "capture_updated", ID: capID, Payload: snap})
		}
	}

	p.forward(w, r.Context(), capID, r.Method, urlStr, headers, bodyCopy)
}

func (p *Proxy) forward(w http.ResponseWriter, ctx context.Context, capID, method, urlStr string, headers map[string][]string, body []byte) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		p.failCapture(capID, "parse url: "+err.Error())
		http.Error(w, "parse url", http.StatusBadRequest)
		return
	}

	upstreamURL := *p.upstream
	upstreamURL.Path = singleSlashJoin(p.upstream.Path, parsed.Path)
	upstreamURL.RawQuery = parsed.RawQuery

	snap, _ := p.store.Update(capID, func(c *Capture) { c.Status = StatusForwarded })
	p.store.Broadcast(Event{Type: "capture_updated", ID: capID, Payload: snap})

	req, err := http.NewRequestWithContext(ctx, method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		p.failCapture(capID, "build req: "+err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for k, vs := range headers {
		if isHopByHop(k) || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Accept-Encoding") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Host = upstreamURL.Host

	started := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		p.failCapture(capID, "upstream: "+err.Error())
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}

	respHeaders := cloneHeaders(resp.Header)
	p.rules.ApplyResponseHeaders(respHeaders)

	ct := resp.Header.Get("Content-Type")
	streaming := strings.Contains(strings.ToLower(ct), "text/event-stream")

	snap, _ = p.store.Update(capID, func(c *Capture) {
		c.Response = &CapturedResponse{
			Status:    resp.StatusCode,
			Headers:   respHeaders,
			Streaming: streaming,
		}
		c.Status = StatusStreaming
	})
	p.store.Broadcast(Event{Type: "response_started", ID: capID, Payload: snap})

	// AUP fallback is only relevant for streaming /v1/messages 200 responses,
	// and only if we haven't already retried once.
	_, alreadyFallback := ctx.Value(fallbackAttemptKey{}).(bool)
	canFallback := p.fallback.Enabled() &&
		!alreadyFallback &&
		strings.EqualFold(method, "POST") &&
		strings.HasSuffix(parsed.Path, "/v1/messages") &&
		resp.StatusCode == http.StatusOK &&
		streaming

	if canFallback {
		p.streamWithFallback(w, ctx, capID, method, urlStr, headers, body, resp, respHeaders, started)
	} else {
		p.streamPassthrough(w, capID, resp, respHeaders, started)
	}
}

// streamPassthrough writes upstream response headers + body directly to w,
// recording each chunk in the capture as it goes.
func (p *Proxy) streamPassthrough(w http.ResponseWriter, capID string, resp *http.Response, respHeaders map[string][]string, started time.Time) {
	defer resp.Body.Close()

	for k, vs := range respHeaders {
		if isHopByHop(k) || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Content-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	ct := resp.Header.Get("Content-Type")
	streaming := strings.Contains(strings.ToLower(ct), "text/event-stream")

	var fullBody bytes.Buffer
	if streaming {
		buf := make([]byte, 4096)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				p.consumeChunk(w, flusher, capID, buf[:n], &fullBody)
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				p.failCapture(capID, "stream read: "+rerr.Error())
				return
			}
		}
	} else {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			p.failCapture(capID, "read body: "+err.Error())
			return
		}
		p.rules.ApplyResponseBody(&respBody)
		if _, werr := w.Write(respBody); werr != nil {
			log.Printf("client write error (capture=%s): %v", capID, werr)
		}
		if flusher != nil {
			flusher.Flush()
		}
		fullBody.Write(respBody)
	}

	end := time.Now()
	durMs := end.Sub(started).Milliseconds()
	bodyStr := fullBody.String()
	snap, _ := p.store.Update(capID, func(c *Capture) {
		c.Status = StatusCompleted
		c.EndedAt = &end
		c.DurationMs = durMs
		if c.Response != nil {
			c.Response.Body = bodyStr
		}
	})
	p.store.Broadcast(Event{Type: "capture_completed", ID: capID, Payload: snap})
}

// consumeChunk applies response rules, writes the chunk to w, and records it in the capture.
func (p *Proxy) consumeChunk(w http.ResponseWriter, flusher http.Flusher, capID string, raw []byte, fullBody *bytes.Buffer) {
	chunk := append([]byte(nil), raw...)
	p.rules.ApplyResponseBody(&chunk)

	if _, werr := w.Write(chunk); werr != nil {
		log.Printf("client write error (capture=%s): %v", capID, werr)
	}
	if flusher != nil {
		flusher.Flush()
	}

	fullBody.Write(chunk)
	sc := StreamChunk{At: time.Now(), Data: string(chunk)}
	p.store.Update(capID, func(c *Capture) {
		if c.Response != nil {
			c.Response.Chunks = append(c.Response.Chunks, sc)
		}
	})
	chunkPayload, _ := json.Marshal(sc)
	p.store.Broadcast(Event{Type: "response_chunk", ID: capID, Payload: chunkPayload})
}

// streamWithFallback reads the upstream SSE stream, looking for an early
// `stop_reason: "refusal"` event. If found, it suppresses the refusal,
// rewrites the request body to use the configured fallback model, and
// recurses into forward() with a fallback marker so the new attempt is the
// final attempt (no second retry).
//
// If `content_block_start` arrives before any refusal, we commit to the
// upstream response: flush the buffered prefix to the client and switch to
// passthrough streaming for the rest.
func (p *Proxy) streamWithFallback(
	w http.ResponseWriter, ctx context.Context, capID, method, urlStr string,
	headers map[string][]string, body []byte,
	resp *http.Response, respHeaders map[string][]string, started time.Time,
) {
	const maxPeek = 32 * 1024 // give up the gamble after this much

	var prefix bytes.Buffer
	var fullBody bytes.Buffer
	buf := make([]byte, 4096)

	decision := ""
	for decision == "" {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			prefix.Write(buf[:n])
			fullBody.Write(buf[:n])
			// record chunks so the user can see what came back even if we
			// end up trashing it for the fallback path
			sc := StreamChunk{At: time.Now(), Data: string(buf[:n])}
			p.store.Update(capID, func(c *Capture) {
				if c.Response != nil {
					c.Response.Chunks = append(c.Response.Chunks, sc)
				}
			})
			chunkPayload, _ := json.Marshal(sc)
			p.store.Broadcast(Event{Type: "response_chunk", ID: capID, Payload: chunkPayload})

			d := classifyStreamPrefix(prefix.Bytes())
			if d != "" {
				decision = d
			} else if prefix.Len() >= maxPeek {
				decision = "passthrough"
			}
		}
		if rerr == io.EOF {
			if decision == "" {
				decision = "passthrough"
			}
			break
		}
		if rerr != nil {
			resp.Body.Close()
			p.failCapture(capID, "fallback peek: "+rerr.Error())
			http.Error(w, "upstream stream error", http.StatusBadGateway)
			return
		}
	}

	switch decision {
	case "refusal":
		// Don't write anything to the client. Drain the rest of the upstream
		// body into the capture (it's short — message_delta + message_stop),
		// then issue the fallback.
		drainRest(resp.Body, &fullBody, &prefix, p, capID)
		resp.Body.Close()

		end := time.Now()
		bodyStr := fullBody.String()
		snap, _ := p.store.Update(capID, func(c *Capture) {
			c.Status = StatusAUPRefused
			c.EndedAt = &end
			c.DurationMs = end.Sub(started).Milliseconds()
			if c.Response != nil {
				c.Response.Body = bodyStr
			}
		})
		p.store.Broadcast(Event{Type: "capture_completed", ID: capID, Payload: snap})

		newBody, newHeaders, err := prepareFallback(body, headers, p.fallback.Model())
		if err != nil {
			log.Printf("fallback (capture=%s): rewrite failed: %v; serving original refusal", capID, err)
			p.serveBufferedRefusal(w, respHeaders, resp.StatusCode, prefix.Bytes())
			return
		}

		log.Printf("fallback (capture=%s): AUP refusal, retrying with %s", capID, p.fallback.Model())

		newCtx := context.WithValue(ctx, fallbackAttemptKey{}, true)
		newCtx = context.WithValue(newCtx, fallbackOriginKey{}, capID)

		newID := newID()
		p.store.Update(capID, func(c *Capture) { c.FallbackTo = newID })

		p.forwardWithID(w, newCtx, newID, method, urlStr, newHeaders, newBody)
		return

	case "passthrough":
		// Real content (or unknown but no refusal seen): flush buffered prefix,
		// continue streaming the rest.
		for k, vs := range respHeaders {
			if isHopByHop(k) || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Content-Encoding") {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		if prefix.Len() > 0 {
			// We already recorded these chunks in the store while peeking,
			// so don't re-record — just send to the client.
			if _, werr := w.Write(prefix.Bytes()); werr != nil {
				log.Printf("client write error (capture=%s): %v", capID, werr)
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Stream the rest, recording each chunk normally.
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				p.consumeChunk(w, flusher, capID, buf[:n], &fullBody)
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				p.failCapture(capID, "stream read: "+rerr.Error())
				resp.Body.Close()
				return
			}
		}
		resp.Body.Close()

		end := time.Now()
		bodyStr := fullBody.String()
		snap, _ := p.store.Update(capID, func(c *Capture) {
			c.Status = StatusCompleted
			c.EndedAt = &end
			c.DurationMs = end.Sub(started).Milliseconds()
			if c.Response != nil {
				c.Response.Body = bodyStr
			}
		})
		p.store.Broadcast(Event{Type: "capture_completed", ID: capID, Payload: snap})
	}
}

// drainRest reads the remainder of the upstream body into both buffers,
// recording chunks in the capture store.
func drainRest(body io.Reader, fullBody, prefix *bytes.Buffer, p *Proxy, capID string) {
	buf := make([]byte, 4096)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			fullBody.Write(buf[:n])
			prefix.Write(buf[:n])
			sc := StreamChunk{At: time.Now(), Data: string(buf[:n])}
			p.store.Update(capID, func(c *Capture) {
				if c.Response != nil {
					c.Response.Chunks = append(c.Response.Chunks, sc)
				}
			})
			payload, _ := json.Marshal(sc)
			p.store.Broadcast(Event{Type: "response_chunk", ID: capID, Payload: payload})
		}
		if rerr != nil {
			return
		}
	}
}

// serveBufferedRefusal is the safe-fallback path when we detected a refusal
// but couldn't proceed with the retry (e.g., model rewrite failed). We just
// send the upstream refusal back to the client as if nothing happened.
func (p *Proxy) serveBufferedRefusal(w http.ResponseWriter, respHeaders map[string][]string, status int, prefix []byte) {
	for k, vs := range respHeaders {
		if isHopByHop(k) || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Content-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	w.Write(prefix)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// classifyStreamPrefix returns "refusal", "passthrough", or "" (need more bytes).
func classifyStreamPrefix(prefix []byte) string {
	if bytes.Contains(prefix, []byte(`"stop_reason":"refusal"`)) {
		return "refusal"
	}
	// content_block_start arrives when real content begins
	if bytes.Contains(prefix, []byte("event: content_block_start")) {
		return "passthrough"
	}
	// Also commit if we see a non-refusal stop_reason early (rare but possible
	// for empty responses on tool use, etc.)
	if bytes.Contains(prefix, []byte(`"stop_reason":"end_turn"`)) ||
		bytes.Contains(prefix, []byte(`"stop_reason":"tool_use"`)) ||
		bytes.Contains(prefix, []byte(`"stop_reason":"max_tokens"`)) ||
		bytes.Contains(prefix, []byte(`"stop_reason":"stop_sequence"`)) {
		return "passthrough"
	}
	return ""
}

// prepareFallback rewrites the request body and headers to make the
// fallback request acceptable to the target model. Returns deep clones
// (caller can keep using the originals).
//
// Patches applied:
//   - body.model → newModel
//   - body.output_config.effort = "xhigh" → "max"
//     (Opus 4.7 supports xhigh; Sonnet 4.6 only low/medium/high/max)
//   - strip Anthropic-Beta tokens that require extra usage tier:
//     - context-1m-2025-08-07 (1M context window, billed extra)
//
// We deliberately leave `thinking`, `context_management`, tools, and all
// other beta flags untouched — they're cross-model on standard tier.
func prepareFallback(body []byte, headers map[string][]string, newModel string) ([]byte, map[string][]string, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, nil, err
	}
	obj["model"] = newModel

	if oc, ok := obj["output_config"].(map[string]any); ok {
		if effort, ok := oc["effort"].(string); ok && effort == "xhigh" {
			oc["effort"] = "max"
		}
	}

	newBody, err := json.Marshal(obj)
	if err != nil {
		return nil, nil, err
	}

	newHeaders := make(map[string][]string, len(headers))
	for k, vs := range headers {
		cp := append([]string(nil), vs...)
		if strings.EqualFold(k, "Anthropic-Beta") {
			cp = stripBetaTokens(cp, "context-1m-2025-08-07")
		}
		newHeaders[k] = cp
	}

	return newBody, newHeaders, nil
}

// stripBetaTokens removes the named comma-separated tokens from each
// Anthropic-Beta header value. Returns the cleaned slice.
func stripBetaTokens(values []string, tokensToRemove ...string) []string {
	remove := make(map[string]struct{}, len(tokensToRemove))
	for _, t := range tokensToRemove {
		remove[t] = struct{}{}
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		parts := strings.Split(v, ",")
		kept := parts[:0]
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if _, drop := remove[t]; drop {
				continue
			}
			if t != "" {
				kept = append(kept, t)
			}
		}
		if len(kept) > 0 {
			out = append(out, strings.Join(kept, ","))
		}
	}
	return out
}

// forwardWithID is forward() but lets the caller fix the capture ID. Used by
// the fallback path which needs to publish a "fallback_to" link on the
// original capture *before* starting the new one.
func (p *Proxy) forwardWithID(w http.ResponseWriter, ctx context.Context, fixedID, method, urlStr string, headers map[string][]string, body []byte) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		http.Error(w, "parse url: "+err.Error(), http.StatusBadRequest)
		return
	}

	now := time.Now()
	initial := &Capture{
		ID:        fixedID,
		CreatedAt: now,
		StartedAt: now,
		Status:    StatusPending,
		Request: CapturedRequest{
			Method:  method,
			URL:     urlStr,
			Path:    parsed.Path,
			Headers: cloneHeaders(headers),
			Body:    string(body),
		},
	}
	if v, ok := ctx.Value(fallbackOriginKey{}).(string); ok {
		initial.FallbackOf = v
	}
	p.store.Add(initial)
	if snap, ok := p.store.Snapshot(fixedID); ok {
		p.store.Broadcast(Event{Type: "capture_started", ID: fixedID, Payload: snap})
	}

	p.forward(w, ctx, fixedID, method, urlStr, headers, body)
}

func (p *Proxy) failCapture(capID, msg string) {
	end := time.Now()
	snap, _ := p.store.Update(capID, func(c *Capture) {
		c.Status = StatusErrored
		c.Error = msg
		c.EndedAt = &end
	})
	p.store.Broadcast(Event{Type: "capture_errored", ID: capID, Payload: snap})
}

func cloneHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

var hopByHop = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailers":            {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func isHopByHop(h string) bool {
	_, ok := hopByHop[http.CanonicalHeaderKey(h)]
	return ok
}

func singleSlashJoin(a, b string) string {
	switch {
	case a == "" || a == "/":
		if !strings.HasPrefix(b, "/") {
			return "/" + b
		}
		return b
	case strings.HasSuffix(a, "/") && strings.HasPrefix(b, "/"):
		return a + b[1:]
	case !strings.HasSuffix(a, "/") && !strings.HasPrefix(b, "/"):
		return a + "/" + b
	default:
		return a + b
	}
}

func pathOf(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	return parsed.Path
}
