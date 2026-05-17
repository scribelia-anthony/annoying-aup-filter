// Package store keeps every captured request/response in a bounded
// in-memory ring buffer and broadcasts state changes to subscribers (the
// SSE event stream consumed by the web UI).
package store

import (
	"encoding/json"
	"sync"
	"time"
)

type CaptureStatus string

const (
	StatusPending     CaptureStatus = "pending"
	StatusIntercepted CaptureStatus = "intercepted"
	StatusForwarded   CaptureStatus = "forwarded"
	StatusStreaming   CaptureStatus = "streaming"
	StatusCompleted   CaptureStatus = "completed"
	StatusDropped     CaptureStatus = "dropped"
	StatusErrored     CaptureStatus = "errored"
	StatusAUPRefused  CaptureStatus = "aup_refused"
)

type CapturedRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type CapturedResponse struct {
	Status    int                 `json:"status"`
	Headers   map[string][]string `json:"headers"`
	Body      string              `json:"body"`
	Streaming bool                `json:"streaming"`
	Chunks    []StreamChunk       `json:"chunks,omitempty"`
}

type StreamChunk struct {
	At   time.Time `json:"at"`
	Data string    `json:"data"`
}

type Capture struct {
	ID         string            `json:"id"`
	CreatedAt  time.Time         `json:"created_at"`
	StartedAt  time.Time         `json:"started_at"`
	EndedAt    *time.Time        `json:"ended_at,omitempty"`
	DurationMs int64             `json:"duration_ms,omitempty"`
	Status     CaptureStatus     `json:"status"`
	Request    CapturedRequest   `json:"request"`
	Response   *CapturedResponse `json:"response,omitempty"`
	Error      string            `json:"error,omitempty"`
	Modified   bool              `json:"modified"`
	ReplayOf   string            `json:"replay_of,omitempty"`
	FallbackOf string            `json:"fallback_of,omitempty"`
	FallbackTo string            `json:"fallback_to,omitempty"`
	Note       string            `json:"note,omitempty"`
}

type Event struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	captures map[string]*Capture
	order    []string
	max      int

	subsMu sync.RWMutex
	subs   map[chan Event]struct{}
}

func New(maxCaptures int) *Store {
	return &Store{
		captures: make(map[string]*Capture),
		order:    make([]string, 0),
		max:      maxCaptures,
		subs:     make(map[chan Event]struct{}),
	}
}

func (s *Store) Add(c *Capture) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captures[c.ID] = c
	s.order = append(s.order, c.ID)
	for len(s.order) > s.max {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.captures, old)
	}
}

// Update mutates a capture under the store lock and returns a JSON snapshot.
// The returned RawMessage is safe to share — it captures the state at the
// moment of the call, not a live pointer to mutable state.
func (s *Store) Update(id string, fn func(*Capture)) (json.RawMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.captures[id]
	if !ok {
		return nil, false
	}
	if fn != nil {
		fn(c)
	}
	data, _ := json.Marshal(c)
	return data, true
}

// Mutate is Update without the snapshot marshal — for hot paths (per-chunk
// streaming appends) where the caller does not need a full snapshot and a
// per-chunk marshal of the whole capture would be O(N²).
func (s *Store) Mutate(id string, fn func(*Capture)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.captures[id]
	if !ok {
		return false
	}
	if fn != nil {
		fn(c)
	}
	return true
}

func (s *Store) Snapshot(id string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.captures[id]
	if !ok {
		return nil, false
	}
	data, _ := json.Marshal(c)
	return data, true
}

// Capture returns a decoded clone — modifying it never races with the store.
func (s *Store) Capture(id string) (*Capture, bool) {
	snap, ok := s.Snapshot(id)
	if !ok {
		return nil, false
	}
	var out Capture
	if err := json.Unmarshal(snap, &out); err != nil {
		return nil, false
	}
	return &out, true
}

func (s *Store) SnapshotAll() json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Capture, 0, len(s.order))
	for _, id := range s.order {
		list = append(list, s.captures[id])
	}
	data, _ := json.Marshal(list)
	return data
}

// SnapshotAllSummaries returns a JSON array of every capture with the
// heavy fields (request.body, response.body, response.chunks) elided.
// Full detail remains available via Snapshot(id) on demand.
//
// Marshalling the full ring buffer with bodies is O(total captured
// bytes); under a 1000-capture ring with multi-MB streaming responses
// this can allocate gigabytes of JSON while holding the store lock,
// blocking every writer and driving the proxy OOM. Summaries keep the
// lock hold proportional to capture count, not payload size.
func (s *Store) SnapshotAllSummaries() json.RawMessage {
	s.mu.RLock()
	list := make([]*Capture, 0, len(s.order))
	for _, id := range s.order {
		c := s.captures[id]
		if c == nil {
			continue
		}
		cc := *c
		cc.Request.Body = ""
		if c.Response != nil {
			rr := *c.Response
			rr.Body = ""
			rr.Chunks = nil
			cc.Response = &rr
		}
		list = append(list, &cc)
	}
	s.mu.RUnlock()
	data, _ := json.Marshal(list)
	return data
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captures = make(map[string]*Capture)
	s.order = s.order[:0]
}

func (s *Store) Subscribe() chan Event {
	ch := make(chan Event, 128)
	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()
	return ch
}

// SubsCount returns the number of live SSE subscribers. Useful for
// tests and operational metrics — a goroutine leak in handleEvents
// shows up here as monotonic growth.
func (s *Store) SubsCount() int {
	s.subsMu.RLock()
	defer s.subsMu.RUnlock()
	return len(s.subs)
}

func (s *Store) Unsubscribe(ch chan Event) {
	s.subsMu.Lock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
	s.subsMu.Unlock()
}

func (s *Store) Broadcast(e Event) {
	s.subsMu.RLock()
	defer s.subsMu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
			// Subscriber too slow — drop the event for that client.
		}
	}
}
