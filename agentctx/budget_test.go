package agentctx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mars/marspi-core/llm"
)

func TestAutoCompactHonorsContextAndBudget(t *testing.T) {
	for _, name := range []string{"cancel", "timeout", "zero_retries"} {
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if requests.Add(1) == 1 {
					close(started)
				}
				if name == "zero_retries" {
					http.Error(w, "retryable", http.StatusServiceUnavailable)
					return
				}
				select {
				case <-req.Context().Done():
				case <-release:
				}
			}))
			defer srv.Close()
			defer close(release)
			p := llm.NewProvider("test", srv.URL, "test")
			m := New(1, p, nil, nil)
			m.AppendSystem("business prompt")
			m.AppendUser("preserve this input")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			zero := 0
			opts := llm.RequestOptions{Timeout: time.Second, MaxRetries: &zero}
			if name == "timeout" {
				opts.Timeout = 80 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() {
				_, err := m.PrepareForAPIContext(ctx, opts)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("compaction request did not start")
			}
			if name == "cancel" {
				cancel()
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("compaction did not honor request budget")
			}
			switch name {
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case "timeout":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("lost timeout: %v", err)
				}
			case "zero_retries":
				if err != nil || m.continuousFails != 1 {
					t.Fatalf("ordinary compact failure should fall back to history: err=%v failures=%d", err, m.continuousFails)
				}
			}
			if requests.Load() != 1 || len(m.Messages) != 2 || m.Messages[1]["content"] != "preserve this input" {
				t.Fatalf("requests=%d history=%v", requests.Load(), m.Messages)
			}
		})
	}
}

func TestCustomCompactPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		messages := body["messages"].([]any)
		if messages[len(messages)-1].(map[string]any)["content"] != "preserve domain decisions" {
			t.Errorf("custom compact prompt missing: %v", messages)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "domain summary"},
		}}})
	}))
	defer srv.Close()
	p := llm.NewProvider("test", srv.URL, "test")
	m := New(100_000, p, nil, nil)
	m.AppendSystem("business prompt")
	m.AppendUser("question")
	m.CompactPrompt = "preserve domain decisions"
	if err := m.FullCompactContext(context.Background(), llm.RequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(m.Messages) != 2 || m.Messages[0]["content"] != "business prompt" || m.Messages[1]["content"] != "domain summary" {
		t.Fatalf("unexpected summary history: %v", m.Messages)
	}
}
