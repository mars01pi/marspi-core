package llm

import (
	"testing"
	"time"
)

func TestProviderOptions(t *testing.T) {
	legacy := NewProvider("deepseek-test", "https://example.invalid/v1", "test").BuildBody(nil, nil)
	if legacy["reasoning_effort"] != "max" || legacy["thinking"].(map[string]any)["type"] != "enabled" {
		t.Fatalf("legacy defaults changed: %v", legacy)
	}
	generic, err := NewProviderWithOptions("deepseek-test", "https://example.invalid/v1", "test", ProviderOptions{Protocol: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if body := generic.BuildBody(nil, nil); body["thinking"] != nil || body["reasoning_effort"] != nil {
		t.Fatalf("generic protocol injected reasoning defaults: %v", body)
	}
	parameters := map[string]any{
		"thinking": map[string]any{"type": "disabled"}, "reasoning_effort": nil, "temperature": 0.2,
	}
	p, err := NewProviderWithOptions("deepseek-test", "https://example.invalid/v1", "test", ProviderOptions{Parameters: parameters})
	if err != nil {
		t.Fatal(err)
	}
	parameters["thinking"].(map[string]any)["type"] = "enabled"
	body := p.BuildBody([]Message{{"role": "system", "content": "business prompt"}}, nil)
	if body["thinking"].(map[string]any)["type"] != "disabled" || body["reasoning_effort"] != nil || body["temperature"] != 0.2 {
		t.Fatalf("incorrect overrides: %v", body)
	}
	body["thinking"].(map[string]any)["type"] = "changed"
	if p.BuildBody(nil, nil)["thinking"].(map[string]any)["type"] != "disabled" {
		t.Fatal("request mutated provider options")
	}
}

func TestInvalidProviderOptions(t *testing.T) {
	for _, opts := range []ProviderOptions{
		{Protocol: "unknown"},
		{Parameters: map[string]any{"messages": nil}},
		{Parameters: map[string]any{"tools": nil}},
		{Parameters: map[string]any{"bad": make(chan int)}},
	} {
		if _, err := NewProviderWithOptions("test", "https://example.invalid", "test", opts); err == nil {
			t.Fatalf("accepted invalid options: %#v", opts)
		}
	}
}

func TestRequestOptionsDefaultsAndZeroRetries(t *testing.T) {
	if timeout, retries := (RequestOptions{}).Limits(); timeout != 300*time.Second || retries != 3 {
		t.Fatalf("defaults: %v %d", timeout, retries)
	}
	zero := 0
	if timeout, retries := (RequestOptions{Timeout: time.Second, MaxRetries: &zero}).Limits(); timeout != time.Second || retries != 0 {
		t.Fatalf("explicit options: %v %d", timeout, retries)
	}
}

func TestMalformedToolArgumentsArePreserved(t *testing.T) {
	for _, args := range []any{`{"x":`, `[]`, `null`, 42, map[string]any{"x": 1}} {
		calls := normalizeToolCalls(Message{"tool_calls": []any{map[string]any{
			"id": "bad", "function": map[string]any{"name": "tool", "arguments": args},
		}}})
		if len(calls) != 1 || calls[0].ArgumentsError == "" {
			t.Fatalf("invalid arguments were lost: %v -> %+v", args, calls)
		}
	}
}
