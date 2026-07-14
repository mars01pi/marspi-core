package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mars/marspi-core/agentctx"
	"github.com/mars/marspi-core/llm"
	"github.com/mars/marspi-core/logx"
	"github.com/mars/marspi-core/tool"
)

// ErrMissingAPIKey is returned when the provider has no API key configured.
var ErrMissingAPIKey = errors.New("MARS_KEY is not set")

// Runner 承载运行一次 agent_loop 所需的依赖。
type Runner struct {
	Provider   llm.Provider
	Registry   *tool.Registry
	Events     *Emitter
	MaxContext int
	MaxIter    int
	Stream     bool // MARS_STREAM
	// ToolMeta optionally builds execution metadata for each tool call.
	// If nil, tools still receive runCtx cancellation via ExecuteQuietCtx.
	ToolMeta func(ctx context.Context, toolName, toolCallID string) tool.ExecutionMeta
}

// Loop 运行一次完整的 agent_loop：追加用户输入，迭代调用模型与工具直至结束。
// Errors from LoopCtx are discarded (CLI fire-and-forget); prefer LoopCtx.
func (r *Runner) Loop(ctx *agentctx.Manager, ctxFilePath, userInput string) {
	_ = r.LoopCtx(context.Background(), ctx, ctxFilePath, userInput)
}

// LoopCtx 与 Loop 相同，但可通过 ctx 在迭代间隙取消，并返回终止原因。
// 正常完成（含 attempt_completion / stop）返回 nil；取消返回 context 错误。
func (r *Runner) LoopCtx(runCtx context.Context, ctx *agentctx.Manager, ctxFilePath, userInput string) error {
	ctx.AppendUser(userInput)
	tools := []map[string]any(nil)
	if r.Registry != nil {
		tools = r.Registry.Schemas()
	}
	logx.Debugf("agent loop start: model=%s tools=%d stream=%v", r.Provider.Model(), len(tools), r.Stream)

	r.emit(Event{Type: EventRunStart, UserInput: userInput})

	var runErr error
	iteration := 0
	for {
		if err := runCtx.Err(); err != nil {
			r.emit(Event{Type: EventWarn, Text: "Stopped."})
			runErr = err
			break
		}
		if strings.TrimSpace(r.Provider.APIKey()) == "" {
			msg := "MARS_KEY is not set (required)\n  export MARS_KEY=sk-your-key"
			r.emit(Event{Type: EventError, Text: msg})
			runErr = ErrMissingAPIKey
			break
		}

		iteration++
		r.emit(Event{Type: EventTurnStart, Iteration: iteration})
		r.emit(Event{Type: EventLLMStart, Text: llmSpinnerText()})

		msgs := ctx.PrepareForAPI()
		logx.Debugf("request iteration=%d messages=%d", iteration, len(msgs))

		resp, err := r.callLLM(runCtx, msgs, tools, iteration)
		r.emit(Event{Type: EventLLMEnd, Iteration: iteration, Usage: resp.Usage,
			ContextTokens: ctx.TotalTokens(), MaxContext: r.MaxContext})

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				r.emit(Event{Type: EventWarn, Text: "Stopped."})
				runErr = err
			} else {
				r.emit(Event{Type: EventError, Text: "request failed: " + err.Error()})
				runErr = fmt.Errorf("llm request: %w", err)
			}
			break
		}

		if resp.FinishReason == "error" {
			r.emit(Event{Type: EventError, Text: resp.Content})
			runErr = fmt.Errorf("model error: %s", resp.Content)
			break
		}

		ctx.AppendAssistant(resp.RawMessage)
		r.emit(Event{Type: EventTurnEnd, Iteration: iteration})

		if resp.Content == "" && resp.ReasoningContent == "" && !resp.HasToolCalls {
			msg := formatEmptyResponse(resp, r.Provider.Model())
			r.emit(Event{Type: EventError, Text: msg})
			runErr = errors.New(msg)
			break
		}

		if resp.FinishReason == "stop" {
			break
		}
		if resp.HasToolCalls {
			completed := false
			for _, tc := range resp.ToolCalls {
				if err := runCtx.Err(); err != nil {
					r.emit(Event{Type: EventWarn, Text: "Stopped."})
					runErr = err
					completed = true
					break
				}
				logx.Debugf("tool call: %s", tc.Name)
				result, done := r.executeTool(runCtx, iteration, tc)
				ctx.AppendTool(tc.ID, tc.Name, result)
				if done {
					completed = true
					break
				}
			}
			if completed {
				break
			}
		} else {
			logx.Debugf("loop exit: finish_reason=%q no tool calls", resp.FinishReason)
			break
		}
		if iteration == r.MaxIter {
			break
		}
	}

	r.emit(Event{Type: EventRunEnd})
	if ctxFilePath != "" {
		_ = ctx.Save(ctxFilePath)
	}
	return runErr
}

func (r *Runner) emit(ev Event) {
	if r.Events != nil {
		r.Events.Emit(ev)
	}
}

func formatEmptyResponse(resp llm.Response, model string) string {
	return "empty model response (finish_reason=" + resp.FinishReason + ", model=" + model + ")"
}

func llmSpinnerText() string { return "Request..." }

func toolResultOK(result any) bool {
	if s, ok := result.(string); ok {
		return !strings.HasPrefix(s, "error:") && !strings.HasPrefix(s, "run tool ")
	}
	return true
}
