package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/scribelia-anthony/prompt-cleaner/internal/fallback"
	"github.com/scribelia-anthony/prompt-cleaner/internal/intercept"
	"github.com/scribelia-anthony/prompt-cleaner/internal/proxy"
	"github.com/scribelia-anthony/prompt-cleaner/internal/rules"
	"github.com/scribelia-anthony/prompt-cleaner/internal/store"
)

func newTestAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	st := store.New(10)
	rs := rules.New()
	in := intercept.New()
	fb := fallback.New()
	px, err := proxy.New("https://example.invalid", st, rs, in, fb)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	return New(st, rs, in, fb, px), st
}

func waitFor(t *testing.T, deadline time.Duration, cond func() bool, msg string) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", msg)
}

// TestEventsReleasesOnClientCancel verifies that closing the SSE
// connection on the client side drops the subscriber, even when no
// further events are broadcast.
func TestEventsReleasesOnClientCancel(t *testing.T) {
	a, st := newTestAPI(t)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	// Drain the snapshot so the handler has actually started.
	br := bufio.NewReader(resp.Body)
	gotSnapshot := false
	for !gotSnapshot {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		if strings.HasPrefix(line, "event: snapshot") {
			gotSnapshot = true
		}
	}

	waitFor(t, time.Second, func() bool { return st.SubsCount() == 1 }, "subscriber to register")

	cancel()

	waitFor(t, 2*time.Second, func() bool { return st.SubsCount() == 0 }, "subscriber to be released after client cancel")
}

// TestEventsReleasesOnHalfOpenClient simulates a client that stops
// reading without closing — the next heartbeat must fail under the
// per-write deadline and the goroutine must exit.
func TestEventsReleasesOnHalfOpenClient(t *testing.T) {
	a, st := newTestAPI(t)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	// Read the snapshot, then stop reading and broadcast many large
	// events. The kernel send buffer fills, the next Write hits the
	// 5-second SSE deadline, and the handler must exit. We don't wait
	// the full window — Close() on the underlying body should also
	// trip the handler much faster.
	_, _ = bufio.NewReader(resp.Body).ReadString('\n')
	_ = resp.Body.Close()

	waitFor(t, 8*time.Second, func() bool { return st.SubsCount() == 0 }, "subscriber to be released after client body close")
}
