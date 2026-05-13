package rules

import (
	"testing"
)

func TestReplaceCompilesAllRules(t *testing.T) {
	rs := New()
	err := rs.Replace([]*Rule{
		{Name: "ok", Enabled: true, Phase: PhaseRequest, Target: TargetBody, Match: `haiku`, Replacement: "sonnet"},
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := len(rs.List()); got != 1 {
		t.Fatalf("len=%d want 1", got)
	}
}

func TestReplaceRejectsEmptyMatch(t *testing.T) {
	rs := New()
	if err := rs.Replace([]*Rule{{Name: "bad", Match: ""}}); err == nil {
		t.Fatal("expected error on empty match")
	}
}

func TestReplaceRejectsBadRegex(t *testing.T) {
	rs := New()
	if err := rs.Replace([]*Rule{{Name: "bad", Match: "(unbalanced"}}); err == nil {
		t.Fatal("expected error on bad regex")
	}
}

func TestApplyRequestBody(t *testing.T) {
	rs := New()
	_ = rs.Replace([]*Rule{
		{Name: "model", Enabled: true, Phase: PhaseRequest, Target: TargetBody, Match: `haiku`, Replacement: "sonnet"},
	})

	body := []byte(`{"model":"haiku-3-5"}`)
	urlStr := "/v1/messages"
	if !rs.ApplyRequest(&urlStr, map[string][]string{}, &body) {
		t.Fatal("expected modification")
	}
	if string(body) != `{"model":"sonnet-3-5"}` {
		t.Fatalf("body=%q", body)
	}
}

func TestApplyRequestURLAndHeader(t *testing.T) {
	rs := New()
	_ = rs.Replace([]*Rule{
		{Name: "u", Enabled: true, Phase: PhaseRequest, Target: TargetURL, Match: `v1`, Replacement: "v2"},
		{Name: "h", Enabled: true, Phase: PhaseRequest, Target: TargetHeader, HeaderName: "X-Foo", Match: `bar`, Replacement: "baz"},
	})

	urlStr := "/v1/messages"
	headers := map[string][]string{"X-Foo": {"bar one", "two bar"}}
	body := []byte(``)
	if !rs.ApplyRequest(&urlStr, headers, &body) {
		t.Fatal("expected modification")
	}
	if urlStr != "/v2/messages" {
		t.Fatalf("urlStr=%q", urlStr)
	}
	if headers["X-Foo"][0] != "baz one" || headers["X-Foo"][1] != "two baz" {
		t.Fatalf("headers=%v", headers)
	}
}

func TestDisabledRuleIsSkipped(t *testing.T) {
	rs := New()
	_ = rs.Replace([]*Rule{
		{Name: "off", Enabled: false, Phase: PhaseRequest, Target: TargetBody, Match: `x`, Replacement: "y"},
	})
	body := []byte("xx")
	urlStr := ""
	if rs.ApplyRequest(&urlStr, map[string][]string{}, &body) {
		t.Fatal("disabled rule should not modify")
	}
	if string(body) != "xx" {
		t.Fatalf("body=%q", body)
	}
}

func TestPhaseFilter(t *testing.T) {
	rs := New()
	_ = rs.Replace([]*Rule{
		{Name: "resp-only", Enabled: true, Phase: PhaseResponse, Target: TargetBody, Match: `x`, Replacement: "y"},
	})
	body := []byte("xx")
	urlStr := ""
	if rs.ApplyRequest(&urlStr, map[string][]string{}, &body) {
		t.Fatal("response rule must not fire on request phase")
	}
	if !rs.ApplyResponseBody(&body) {
		t.Fatal("response rule should fire on response body")
	}
	if string(body) != "yy" {
		t.Fatalf("body=%q", body)
	}
}

func TestApplyResponseHeaders(t *testing.T) {
	rs := New()
	_ = rs.Replace([]*Rule{
		{Name: "h", Enabled: true, Phase: PhaseResponse, Target: TargetHeader, HeaderName: "Server", Match: `secret`, Replacement: "redacted"},
	})
	h := map[string][]string{"Server": {"secret/1.0"}, "X-Other": {"unchanged"}}
	if !rs.ApplyResponseHeaders(h) {
		t.Fatal("expected modification")
	}
	if h["Server"][0] != "redacted/1.0" {
		t.Fatalf("Server=%v", h["Server"])
	}
	if h["X-Other"][0] != "unchanged" {
		t.Fatalf("X-Other=%v", h["X-Other"])
	}
}

func TestCaptureGroupReplacement(t *testing.T) {
	rs := New()
	_ = rs.Replace([]*Rule{
		{Name: "model", Enabled: true, Phase: PhaseRequest, Target: TargetBody, Match: `claude-(\w+)-(\d)`, Replacement: "claude-$1-$2-pinned"},
	})
	body := []byte(`"model":"claude-opus-4"`)
	urlStr := ""
	rs.ApplyRequest(&urlStr, map[string][]string{}, &body)
	if string(body) != `"model":"claude-opus-4-pinned"` {
		t.Fatalf("body=%q", body)
	}
}

func TestAutoAssignedIDs(t *testing.T) {
	rs := New()
	_ = rs.Replace([]*Rule{
		{Name: "a", Enabled: true, Phase: PhaseRequest, Target: TargetBody, Match: `x`},
		{Name: "b", ID: "preset", Enabled: true, Phase: PhaseRequest, Target: TargetBody, Match: `y`},
	})
	list := rs.List()
	if list[0].ID == "" {
		t.Fatal("expected auto-assigned id")
	}
	if list[1].ID != "preset" {
		t.Fatalf("preset id lost: %q", list[1].ID)
	}
}
