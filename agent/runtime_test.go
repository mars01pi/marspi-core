package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mars/marspi-core/agentctx"
	"github.com/mars/marspi-core/llm"
	"github.com/mars/marspi-core/tool"
)

type runtimeTool struct {
	tool.Base
	name string
	deny bool
	run  func() tool.Result
}

func (t runtimeTool) Name() string                   { return t.name }
func (t runtimeTool) Description() string            { return "runtime test tool" }
func (t runtimeTool) Params() []tool.Param           { return nil }
func (t runtimeTool) Confirm(map[string]any) bool    { return !t.deny }
func (t runtimeTool) Run(map[string]any) tool.Result { return t.run() }

func toolCall(id, name, args string) map[string]any {
	return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}
}

func TestRunNaturalStopAndPromptInjection(t *testing.T) {
	srv := newMockLLMServer(t, func(w http.ResponseWriter, req *http.Request, _ int) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, exists := body["tools"]; exists {
			t.Error("empty registry exposed tools")
		}
		messages := body["messages"].([]any)
		if len(messages) != 2 || messages[0].(map[string]any)["content"] != "business prompt" {
			t.Errorf("unexpected prompt: %v", messages)
		}
		writeJSONCompletion(w, "stop", map[string]any{"role": "assistant", "content": "answer"})
	})
	defer srv.Close()
	runner, mgr, events := newTestRunner(t, srv.URL)
	runner.Registry = tool.NewEmptyRegistry(nil)
	mgr.AppendSystem("business prompt")
	var eventResult *RunResult
	events.Subscribe(func(ev Event) {
		if ev.Type == EventRunEnd {
			eventResult = ev.Result
		}
	})
	result, err := runner.Run(context.Background(), mgr, "question")
	if err != nil || result.StopReason != StopModel || result.Content != "answer" || result.Iterations != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if eventResult == nil || !reflect.DeepEqual(*eventResult, result) {
		t.Fatalf("run_end=%+v result=%+v", eventResult, result)
	}
}

func TestRunCustomEndTurnPairsAllToolCalls(t *testing.T) {
	srv := newMockLLMServer(t, func(w http.ResponseWriter, req *http.Request, n int) {
		if n == 1 {
			writeJSONCompletion(w, "tool_calls", map[string]any{"role": "assistant", "tool_calls": []any{
				toolCall("before", "work", `{}`),
				toolCall("done", "ask_user", `{}`),
				toolCall("after", "work", `{}`),
			}})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		paired := map[string]bool{}
		for _, raw := range body["messages"].([]any) {
			message := raw.(map[string]any)
			if message["role"] == "tool" {
				paired[message["tool_call_id"].(string)] = true
			}
		}
		if len(paired) != 3 {
			t.Errorf("incomplete tool history: %v", paired)
		}
		writeJSONCompletion(w, "stop", map[string]any{"role": "assistant", "content": "next answer"})
	})
	defer srv.Close()
	runner, mgr, _ := newTestRunner(t, srv.URL)
	runner.Registry = tool.NewEmptyRegistry(nil)
	workCalls := 0
	runner.Registry.Register(runtimeTool{name: "work", run: func() tool.Result {
		workCalls++
		return tool.OK("done")
	}})
	output := map[string]any{"question": "Which option?"}
	runner.Registry.Register(runtimeTool{name: "ask_user", run: func() tool.Result {
		return tool.Result{Success: true, Content: output, EndTurn: true}
	}})
	result, err := runner.Run(context.Background(), mgr, "start")
	if err != nil || result.StopReason != StopCompleted || !reflect.DeepEqual(result.Output, output) || workCalls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, workCalls, err)
	}
	if _, err := runner.Run(context.Background(), mgr, "continue"); err != nil {
		t.Fatal(err)
	}
}

func TestRunInvalidCompletionDoesNotEndTurn(t *testing.T) {
	for _, name := range []string{"missing", "denied", "failed", "empty", "malformed"} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			srv := newMockLLMServer(t, func(w http.ResponseWriter, _ *http.Request, n int) {
				requests.Add(1)
				if n == 1 {
					args := `{"result":""}`
					if name == "malformed" {
						args = `{"result":`
					}
					writeJSONCompletion(w, "tool_calls", map[string]any{"role": "assistant", "tool_calls": []any{
						toolCall("done", "attempt_completion", args),
					}})
					return
				}
				writeJSONCompletion(w, "stop", map[string]any{"role": "assistant", "content": "corrected"})
			})
			defer srv.Close()
			runner, mgr, _ := newTestRunner(t, srv.URL)
			runner.Registry = tool.NewEmptyRegistry(nil)
			switch name {
			case "denied", "failed":
				runner.Registry.Register(runtimeTool{name: "attempt_completion", deny: name == "denied", run: func() tool.Result {
					return tool.Result{Success: false, Content: "failed", EndTurn: true}
				}})
			case "empty", "malformed":
				runner.Registry.Register(tool.NewAttemptCompletionTool())
			}
			result, err := runner.Run(context.Background(), mgr, "start")
			if err != nil || result.StopReason != StopModel || result.Content != "corrected" || requests.Load() != 2 {
				t.Fatalf("result=%+v requests=%d err=%v", result, requests.Load(), err)
			}
		})
	}
}

func TestRunIterationLimit(t *testing.T) {
	srv := newMockLLMServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSONCompletion(w, "tool_calls", map[string]any{"role": "assistant", "tool_calls": []any{
			toolCall("echo", "echo_tool", `{"msg":"hi"}`),
		}})
	})
	defer srv.Close()
	runner, mgr, _ := newTestRunner(t, srv.URL)
	runner.MaxIter = 1
	result, err := runner.Run(context.Background(), mgr, "start")
	if !errors.Is(err, ErrMaxIterations) || result.StopReason != StopMaxIterations || result.Iterations != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := runner.LoopCtx(context.Background(), mgr, "", "again"); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("legacy wrapper hid iteration limit: %v", err)
	}
}

func TestRunCanceledToolPairsRemainingCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := newMockLLMServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSONCompletion(w, "tool_calls", map[string]any{"role": "assistant", "tool_calls": []any{
			toolCall("first", "cancel", `{}`), toolCall("second", "cancel", `{}`),
		}})
	})
	defer srv.Close()
	runner, mgr, _ := newTestRunner(t, srv.URL)
	calls := 0
	runner.Registry.Register(runtimeTool{name: "cancel", run: func() tool.Result {
		calls++
		cancel()
		return tool.Result{Success: true, Content: "done", EndTurn: true}
	}})
	result, err := runner.Run(ctx, mgr, "start")
	if !errors.Is(err, context.Canceled) || result.StopReason != StopCanceled || calls != 1 || mgr.Len() != 4 {
		t.Fatalf("result=%+v calls=%d messages=%d err=%v", result, calls, mgr.Len(), err)
	}
}

func TestRunTimeoutsAndZeroRetries(t *testing.T) {
	for _, name := range []string{"request_timeout", "run_timeout", "retry_timeout", "no_retries", "stream_no_retries"} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			release := make(chan struct{})
			srv := newMockLLMServer(t, func(w http.ResponseWriter, req *http.Request, _ int) {
				requests.Add(1)
				if name == "no_retries" || name == "stream_no_retries" || name == "retry_timeout" {
					http.Error(w, "retryable", http.StatusServiceUnavailable)
					return
				}
				select {
				case <-req.Context().Done():
				case <-release:
				}
			})
			defer srv.Close()
			defer close(release)
			runner, mgr, _ := newTestRunner(t, srv.URL)
			zero := 0
			runner.RequestOptions.MaxRetries = &zero
			switch name {
			case "request_timeout":
				runner.RequestOptions.Timeout = 80 * time.Millisecond
			case "retry_timeout":
				runner.RequestOptions.Timeout = 80 * time.Millisecond
				runner.RequestOptions.MaxRetries = nil
			case "run_timeout":
				runner.RunTimeout = 80 * time.Millisecond
			case "stream_no_retries":
				runner.Stream = true
			}
			result, err := runner.Run(context.Background(), mgr, "start")
			if err == nil || requests.Load() != 1 {
				t.Fatalf("requests=%d result=%+v err=%v", requests.Load(), result, err)
			}
			if name == "request_timeout" || name == "run_timeout" || name == "retry_timeout" {
				if !errors.Is(err, context.DeadlineExceeded) || result.StopReason != StopTimeout {
					t.Fatalf("timeout result=%+v err=%v", result, err)
				}
			} else if result.StopReason != StopError {
				t.Fatalf("failure result=%+v", result)
			}
		})
	}
}

func TestRunRetryBudgetIncludesStreamFallback(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocking", true: "stream"}[stream], func(t *testing.T) {
			var requests atomic.Int32
			srv := newMockLLMServer(t, func(w http.ResponseWriter, req *http.Request, n int) {
				requests.Add(1)
				if n == 1 {
					http.Error(w, "retryable", http.StatusServiceUnavailable)
					return
				}
				if n > 2 {
					t.Errorf("exceeded retry budget: %d", n)
				}
				var body map[string]any
				_ = json.NewDecoder(req.Body).Decode(&body)
				if body["stream"] != false {
					t.Error("fallback must use blocking transport")
				}
				writeJSONCompletion(w, "stop", map[string]any{"role": "assistant", "content": "retried"})
			})
			defer srv.Close()
			runner, mgr, _ := newTestRunner(t, srv.URL)
			retries := 1
			runner.Stream = stream
			runner.RequestOptions = llm.RequestOptions{Timeout: 3 * time.Second, MaxRetries: &retries}
			result, err := runner.Run(context.Background(), mgr, "start")
			if err != nil || requests.Load() != 2 || result.StopReason != StopModel {
				t.Fatalf("requests=%d result=%+v err=%v", requests.Load(), result, err)
			}
		})
	}
}

func TestRunCancellationAtModelStop(t *testing.T) {
	srv := newMockLLMServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSONCompletion(w, "stop", map[string]any{"role": "assistant", "content": "answer"})
	})
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner, mgr, events := newTestRunner(t, srv.URL)
	events.Subscribe(func(ev Event) {
		if ev.Type == EventMessageEnd {
			cancel()
		}
	})
	result, err := runner.Run(ctx, mgr, "start")
	if !errors.Is(err, context.Canceled) || result.StopReason != StopCanceled {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRunRejectsTruncatedAnswer(t *testing.T) {
	srv := newMockLLMServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSONCompletion(w, "length", map[string]any{"role": "assistant", "content": "partial"})
	})
	defer srv.Close()
	runner, mgr, _ := newTestRunner(t, srv.URL)
	result, err := runner.Run(context.Background(), mgr, "start")
	if err == nil || result.StopReason != StopError || result.Content != "partial" || result.FinishReason != "length" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRunNoRegistryHandlesUnknownTool(t *testing.T) {
	srv := newMockLLMServer(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			writeJSONCompletion(w, "tool_calls", map[string]any{"role": "assistant", "tool_calls": []any{toolCall("x", "unknown", `{}`)}})
			return
		}
		writeJSONCompletion(w, "stop", map[string]any{"role": "assistant", "content": "no tools"})
	})
	defer srv.Close()
	p := llm.NewProvider("test", srv.URL, "test")
	runner := &Runner{Provider: p, MaxIter: 2}
	mgr := agentctx.New(100_000, p, nil, nil)
	result, err := runner.Run(context.Background(), mgr, "start")
	if err != nil || result.StopReason != StopModel {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
