package arena

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAPI struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	status  int
	resp    string
	delay   time.Duration
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	f.mu.Lock()
	f.bodies = append(f.bodies, m)
	f.headers = append(f.headers, r.Header.Clone())
	status, resp, delay := f.status, f.resp, f.delay
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, resp)
}

func msgJSON(model, stop string, content string) string {
	return `{"id":"msg_1","type":"message","role":"assistant","model":"` + model + `","content":[` + content +
		`],"stop_reason":"` + stop + `","stop_sequence":null,"usage":{"input_tokens":1500,"output_tokens":800,` +
		`"cache_read_input_tokens":2000,"cache_creation_input_tokens":0}}`
}

const actBlock = `{"type":"thinking","thinking":"","signature":"sig"},{"type":"tool_use","id":"tu_1","name":"act","input":{"action":"e2e4","talk":"hi","memo":"m"}}`

func newTestClient(t *testing.T, f *fakeAPI) *AnthropicClient {
	t.Helper()
	// Environment that must be ignored.
	t.Setenv("ANTHROPIC_API_KEY", "env-key-must-not-be-used")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token-must-not-be-used")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Leak: 1")
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return NewAnthropicClient("explicit-test-key", srv.URL)
}

func testReq() ModelRequest {
	p, _ := LoadPack("chess/v1")
	return ModelRequest{Model: "claude-haiku-5-5", Effort: "medium", MaxTokens: 16000, Tool: p.Tool,
		System: "SYSTEM", User: "USER"}
}

func TestAnthropicRequestShape(t *testing.T) {
	f := &fakeAPI{resp: msgJSON("claude-haiku-5-5", "tool_use", actBlock)}
	c := newTestClient(t, f)
	rep, err := c.Decide(context.Background(), testReq())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Act == nil || rep.Act.Action != "e2e4" || rep.Act.Talk != "hi" || rep.Act.Memo != "m" {
		t.Fatalf("act %+v", rep.Act)
	}
	if rep.Model != "claude-haiku-5-5" || rep.StopReason != "tool_use" {
		t.Fatalf("reply %+v", rep)
	}
	if rep.Usage != (Usage{In: 1500, Out: 800, CacheRead: 2000}) {
		t.Fatalf("usage %+v", rep.Usage)
	}
	b, h := f.bodies[0], f.headers[0]
	if h.Get("X-Api-Key") != "explicit-test-key" {
		t.Fatalf("api key header %q", h.Get("X-Api-Key"))
	}
	if h.Get("Authorization") != "" || h.Get("X-Leak") != "" || h.Get("Anthropic-Beta") != "" {
		t.Fatalf("unexpected headers: auth=%q leak=%q beta=%q", h.Get("Authorization"), h.Get("X-Leak"), h.Get("Anthropic-Beta"))
	}
	for _, k := range []string{"temperature", "top_p", "top_k", "fallbacks", "thinking", "stream"} {
		if _, ok := b[k]; ok {
			t.Fatalf("request must not send %q: %v", k, b[k])
		}
	}
	if b["model"] != "claude-haiku-5-5" || b["max_tokens"] != float64(16000) {
		t.Fatalf("model/max_tokens %v %v", b["model"], b["max_tokens"])
	}
	tools := b["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools %v", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "act" || tool["strict"] != true {
		t.Fatalf("tool %v", tool)
	}
	schema := tool["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false || schema["type"] != "object" {
		t.Fatalf("schema %v", schema)
	}
	tc := b["tool_choice"].(map[string]any)
	if tc["type"] != "auto" || tc["disable_parallel_tool_use"] != true {
		t.Fatalf("tool_choice %v", tc)
	}
	sys := b["system"].([]any)[0].(map[string]any)
	if sys["text"] != "SYSTEM" || sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("system %v", sys)
	}
	if b["output_config"].(map[string]any)["effort"] != "medium" {
		t.Fatalf("output_config %v", b["output_config"])
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("messages must be one fresh user message: %v", msgs)
	}
}

func TestAnthropicThinkingAdaptiveAndNoEffort(t *testing.T) {
	f := &fakeAPI{resp: msgJSON("claude-opus-5-5", "tool_use", actBlock)}
	c := newTestClient(t, f)
	req := testReq()
	req.Model, req.Effort, req.Thinking = "claude-opus-5-5", "", "adaptive"
	if _, err := c.Decide(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	b := f.bodies[0]
	if b["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Fatalf("thinking %v", b["thinking"])
	}
	if _, ok := b["output_config"]; ok {
		t.Fatalf("effort unset must omit output_config: %v", b["output_config"])
	}
}

func TestAnthropicReplyWithoutAct(t *testing.T) {
	for _, c := range []struct{ stop, content string }{
		{"end_turn", `{"type":"text","text":"I play e4"}`},
		{"max_tokens", `{"type":"thinking","thinking":"","signature":"s"}`},
		{"refusal", `{"type":"text","text":""}`},
		{"tool_use", `{"type":"tool_use","id":"t","name":"other","input":{}}`},
	} {
		f := &fakeAPI{resp: msgJSON("claude-haiku-5-5", c.stop, c.content)}
		rep, err := newTestClient(t, f).Decide(context.Background(), testReq())
		if err != nil {
			t.Fatal(err)
		}
		if rep.Act != nil || rep.StopReason != c.stop {
			t.Fatalf("%s: %+v", c.stop, rep)
		}
	}
}

func TestAnthropicErrorKinds(t *testing.T) {
	cases := []struct {
		status int
		typ    string
		want   ModelErrorKind
	}{
		{429, "rate_limit_error", ModelProvider},
		{529, "overloaded_error", ModelProvider},
		{500, "api_error", ModelProvider},
		{503, "api_error", ModelProvider},
		{400, "invalid_request_error", ModelFatal},
		{401, "authentication_error", ModelFatal},
		{403, "permission_error", ModelFatal},
	}
	for _, c := range cases {
		f := &fakeAPI{status: c.status, resp: `{"type":"error","error":{"type":"` + c.typ + `","message":"x"}}`}
		_, err := newTestClient(t, f).Decide(context.Background(), testReq())
		var me *ModelError
		if !errors.As(err, &me) || me.Kind != c.want || me.Status != c.status {
			t.Fatalf("%d: got %v", c.status, err)
		}
		if len(f.bodies) != 1 {
			t.Fatalf("%d: SDK retried %d times; arena owns retries", c.status, len(f.bodies)-1)
		}
		if strings.Contains(err.Error(), "explicit-test-key") {
			t.Fatal("error text leaks the key")
		}
	}
}

func TestAnthropicTimeout(t *testing.T) {
	f := &fakeAPI{resp: msgJSON("claude-haiku-5-5", "tool_use", actBlock), delay: 2 * time.Second}
	c := newTestClient(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Decide(ctx, testReq())
	var me *ModelError
	if !errors.As(err, &me) || me.Kind != ModelTimeout {
		t.Fatalf("got %v", err)
	}
}

func TestAnthropicCancelIsNotAStrike(t *testing.T) {
	f := &fakeAPI{resp: msgJSON("claude-haiku-5-5", "tool_use", actBlock), delay: 2 * time.Second}
	c := newTestClient(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := c.Decide(ctx, testReq())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel must surface as context.Canceled, got %v", err)
	}
}

func TestAnthropicNetworkErrorIsProvider(t *testing.T) {
	c := NewAnthropicClient("k", "http://127.0.0.1:1")
	_, err := c.Decide(context.Background(), testReq())
	var me *ModelError
	if !errors.As(err, &me) || me.Kind != ModelProvider {
		t.Fatalf("got %v", err)
	}
}

// The arena must never read the SDK's default key variable.
func TestSourceNeverReadsDefaultKeyEnv(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "cmd", "arena")} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		if len(files) == 0 {
			t.Fatalf("no go files in %s", dir)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, _ := os.ReadFile(f)
			s := strings.ReplaceAll(string(b), "ARENA_ANTHROPIC_API_KEY", "")
			for _, bad := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "anthropic.NewClient()", "DefaultClientOptions"} {
				if strings.Contains(s, bad) {
					t.Fatalf("%s mentions %s", f, bad)
				}
			}
		}
	}
	if KeyEnv != "ARENA_ANTHROPIC_API_KEY" {
		t.Fatal(KeyEnv)
	}
}
