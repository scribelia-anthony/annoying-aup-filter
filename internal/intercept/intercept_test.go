package intercept

import (
	"context"
	"testing"
	"time"
)

func TestAwaitGetsDecision(t *testing.T) {
	i := New()
	i.Set(true)

	go func() {
		// Wait for Await to register before posting the decision.
		for {
			if len(i.Pending()) > 0 {
				i.Decide("abc", Decision{Action: ActionForward, URL: "/edited"})
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d, ok := i.Await(ctx, "abc")
	if !ok {
		t.Fatal("Await returned !ok")
	}
	if d.Action != ActionForward || d.URL != "/edited" {
		t.Fatalf("decision=%+v", d)
	}
	if len(i.Pending()) != 0 {
		t.Fatal("Pending should be empty after Decide")
	}
}

func TestAwaitCancelsOnContext(t *testing.T) {
	i := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := i.Await(ctx, "x"); ok {
		t.Fatal("Await should return !ok on cancelled ctx")
	}
}

func TestDecideUnknownReturnsFalse(t *testing.T) {
	i := New()
	if i.Decide("nope", Decision{}) {
		t.Fatal("Decide on unknown id should return false")
	}
}

func TestEnabledDefault(t *testing.T) {
	i := New()
	if i.Enabled() {
		t.Fatal("default should be disabled")
	}
	i.Set(true)
	if !i.Enabled() {
		t.Fatal("Set(true) didn't enable")
	}
}
