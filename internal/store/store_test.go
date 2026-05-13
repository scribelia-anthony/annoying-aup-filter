package store

import (
	"encoding/json"
	"testing"
	"time"
)

func mkCapture(id string) *Capture {
	return &Capture{
		ID:        id,
		CreatedAt: time.Now(),
		StartedAt: time.Now(),
		Status:    StatusPending,
		Request:   CapturedRequest{Method: "POST", URL: "/v1/messages", Path: "/v1/messages"},
	}
}

func TestRingBufferEvictsOldest(t *testing.T) {
	s := New(3)
	for _, id := range []string{"a", "b", "c", "d"} {
		s.Add(mkCapture(id))
	}
	if _, ok := s.Snapshot("a"); ok {
		t.Fatal("oldest should have been evicted")
	}
	for _, id := range []string{"b", "c", "d"} {
		if _, ok := s.Snapshot(id); !ok {
			t.Fatalf("expected %s to remain", id)
		}
	}
}

func TestUpdateMutatesAndReturnsSnapshot(t *testing.T) {
	s := New(10)
	s.Add(mkCapture("x"))
	snap, ok := s.Update("x", func(c *Capture) { c.Status = StatusCompleted })
	if !ok {
		t.Fatal("Update returned !ok")
	}
	var c Capture
	if err := json.Unmarshal(snap, &c); err != nil {
		t.Fatalf("snapshot not valid json: %v", err)
	}
	if c.Status != StatusCompleted {
		t.Fatalf("status=%v", c.Status)
	}
}

func TestUpdateMissingID(t *testing.T) {
	s := New(10)
	if _, ok := s.Update("nope", func(*Capture) {}); ok {
		t.Fatal("expected !ok on missing id")
	}
}

func TestCaptureReturnsClone(t *testing.T) {
	s := New(10)
	s.Add(mkCapture("x"))
	c, _ := s.Capture("x")
	c.Status = StatusDropped
	got, _ := s.Capture("x")
	if got.Status == StatusDropped {
		t.Fatal("mutation leaked into store")
	}
}

func TestSnapshotAllOrdered(t *testing.T) {
	s := New(10)
	for _, id := range []string{"a", "b", "c"} {
		s.Add(mkCapture(id))
	}
	var list []*Capture
	_ = json.Unmarshal(s.SnapshotAll(), &list)
	if len(list) != 3 || list[0].ID != "a" || list[2].ID != "c" {
		t.Fatalf("order wrong: %+v", list)
	}
}

func TestClear(t *testing.T) {
	s := New(10)
	s.Add(mkCapture("x"))
	s.Clear()
	if _, ok := s.Snapshot("x"); ok {
		t.Fatal("Clear left data behind")
	}
}

func TestBroadcastDropsSlowSubscribers(t *testing.T) {
	s := New(10)
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)
	// Fill the 128-slot buffer plus one — the extra must not block.
	done := make(chan struct{})
	go func() {
		for range 200 {
			s.Broadcast(Event{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on slow subscriber")
	}
}
