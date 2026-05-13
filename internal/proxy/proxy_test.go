package proxy

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestClassifyStreamPrefix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"refusal", `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"refusal"}}`, "refusal"},
		{"content_block_start", "event: content_block_start\ndata: {}", "passthrough"},
		{"end_turn", `"stop_reason":"end_turn"`, "passthrough"},
		{"tool_use", `"stop_reason":"tool_use"`, "passthrough"},
		{"unknown", `event: ping`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyStreamPrefix([]byte(c.in)); got != c.want {
				t.Fatalf("classify(%q)=%q want %q", c.in, got, c.want)
			}
		})
	}
}

func TestPrepareFallbackRewritesModel(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","messages":[]}`)
	out, _, err := PrepareFallback(body, nil, "claude-opus-4-6")
	if err != nil {
		t.Fatalf("PrepareFallback: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("output not json: %v", err)
	}
	if obj["model"] != "claude-opus-4-6" {
		t.Fatalf("model=%v", obj["model"])
	}
}

func TestPrepareFallbackDowngradesEffort(t *testing.T) {
	body := []byte(`{"model":"x","output_config":{"effort":"xhigh"}}`)
	out, _, err := PrepareFallback(body, nil, "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	_ = json.Unmarshal(out, &obj)
	got := obj["output_config"].(map[string]any)["effort"]
	if got != "max" {
		t.Fatalf("effort=%v want max", got)
	}
}

func TestPrepareFallbackKeepsOtherEffortValues(t *testing.T) {
	body := []byte(`{"model":"x","output_config":{"effort":"high"}}`)
	out, _, _ := PrepareFallback(body, nil, "y")
	var obj map[string]any
	_ = json.Unmarshal(out, &obj)
	if got := obj["output_config"].(map[string]any)["effort"]; got != "high" {
		t.Fatalf("effort=%v", got)
	}
}

func TestPrepareFallbackStrips1MContextBeta(t *testing.T) {
	headers := map[string][]string{
		"Anthropic-Beta": {"context-1m-2025-08-07,prompt-caching-2024-07-31"},
	}
	_, out, err := PrepareFallback([]byte(`{}`), headers, "x")
	if err != nil {
		t.Fatal(err)
	}
	got := out["Anthropic-Beta"]
	want := []string{"prompt-caching-2024-07-31"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Anthropic-Beta=%v want %v", got, want)
	}
}

func TestPrepareFallbackDropsHeaderWhenOnlyTokenIsStripped(t *testing.T) {
	headers := map[string][]string{
		"Anthropic-Beta": {"context-1m-2025-08-07"},
	}
	_, out, _ := PrepareFallback([]byte(`{}`), headers, "x")
	if vs, ok := out["Anthropic-Beta"]; ok && len(vs) > 0 {
		t.Fatalf("expected empty Anthropic-Beta, got %v", vs)
	}
}

func TestPrepareFallbackBadJSON(t *testing.T) {
	if _, _, err := PrepareFallback([]byte(`not json`), nil, "x"); err == nil {
		t.Fatal("expected error on bad json")
	}
}

func TestSingleSlashJoin(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "/v1", "/v1"},
		{"/", "v1", "/v1"},
		{"/", "/v1", "/v1"},
		{"/api", "/v1", "/api/v1"},
		{"/api/", "/v1", "/api/v1"},
		{"/api", "v1", "/api/v1"},
		{"/api/", "v1", "/api/v1"},
	}
	for _, c := range cases {
		if got := singleSlashJoin(c.a, c.b); got != c.want {
			t.Errorf("singleSlashJoin(%q,%q)=%q want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestIsHopByHop(t *testing.T) {
	if !isHopByHop("Connection") {
		t.Fatal("Connection should be hop-by-hop")
	}
	if !isHopByHop("connection") {
		t.Fatal("case-insensitive match")
	}
	if isHopByHop("Content-Type") {
		t.Fatal("Content-Type is end-to-end")
	}
}

func TestStripBetaTokens(t *testing.T) {
	got := stripBetaTokens([]string{"a, b , c", "b,b,d"}, "b")
	want := []string{"a,c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}
