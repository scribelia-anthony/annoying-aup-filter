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
	client      *http.Client
}

type replayOriginKey struct{}

func NewProxy(upstream string, store *Store, rules *Rules, interceptor *Interceptor) (*Proxy, error) {
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
		client: &http.Client{
			// no timeout: Anthropic streaming responses can run for minutes
			Timeout: 0,
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
	p.store.Add(initial)
	if snap, ok := p.store.Snapshot(capID); ok {
		p.store.Broadcast(Event{Type: "capture_started", ID: capID, Payload: snap})
	}

	// Apply request-phase rules
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

	// Intercept (if enabled)
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
	defer resp.Body.Close()

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

	var fullBody bytes.Buffer

	if streaming {
		buf := make([]byte, 4096)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
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
	snap, _ = p.store.Update(capID, func(c *Capture) {
		c.Status = StatusCompleted
		c.EndedAt = &end
		c.DurationMs = durMs
		if c.Response != nil {
			c.Response.Body = bodyStr
		}
	})
	p.store.Broadcast(Event{Type: "capture_completed", ID: capID, Payload: snap})
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
