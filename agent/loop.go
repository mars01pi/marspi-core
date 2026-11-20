package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mars/marspi-core/agentctx"
	"github.com/mars/marspi-core/llm"
	"github.com/mars/marspi-core/logx"
	"github.com/mars/marspi-core/tool"
)

// ErrMissingAPIKey is returned when the provider has no API key configured.
var ErrMissingAPIKey = errors.New("model API key is not configured")

// Runner 承载运行一次 agent_loop 所需的依赖。
type Runner struct {
	Provider       llm.Provider
	Registry       *tool.Registry
	Events         *Emitter
	MaxContext     int
	MaxIter        int
	Stream         bool // MARS_STREAM
	RequestOptions llm.RequestOptions
	// RunTimeout 限制整个运行（含压缩、模型调用和支持 context 的工具），0 表示仅依赖调用方 context。
	RunTimeout time.Duration
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
// 正常结束返回 nil；达到迭代上限返回 ErrMaxIterations，取消或超时返回 context 错误。
func (r *Runner) LoopCtx(runCtx context.Context, ctx *agentctx.Manager, ctxFilePath, userInput string) error {
	_, err := r.Run(runCtx, ctx, userInput)
	if ctxFilePath != "" && ctx != nil {
		if saveErr := ctx.Save(ctxFilePath); saveErr != nil {
			return errors.Join(err, fmt.Errorf("save session: %w", saveErr))
		}
	}
	return err
}

// Run 运行一轮并返回明确的结束状态，不读写会话文件、不装配默认 prompt。
// Runner 与 Manager 由调用方独占使用，不支持同实例并发运行。
func (r *Runner) Run(runCtx context.Context, ctx *agentctx.Manager, userInput string) (out RunResult, runErr error) {
	if r.RunTimeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, r.RunTimeout)
		defer cancel()
	}
	out.StopReason = StopError
	r.emit(Event{Type: EventRunStart, UserInput: userInput})
	defer func() {
		if errors.Is(runErr, context.Canceled) {
			out.StopReason = StopCanceled
		} else if errors.Is(runErr, context.DeadlineExceeded) {
			out.StopReason = StopTimeout
		}
		result := out
		r.emit(Event{Type: EventRunEnd, Result: &result})
	}()
	if ctx == nil || r.Provider == nil {
		return out, errors.New("agent requires a context manager and provider")
	}
	ctx.AppendUser(userInput)
	tools := []map[string]any(nil)
	if r.Registry != nil {
		tools, runErr = r.Registry.SchemasChecked()
		if runErr != nil {
			return out, runErr
		}
	}
	logx.Debugf("agent loop start: model=%s tools=%d stream=%v", r.Provider.Model(), len(tools), r.Stream)

	iteration := 0
	for {
		if err := runCtx.Err(); err != nil {
			r.emit(Event{Type: EventWarn, Text: "Stopped."})
			runErr = err
			break
		}
		if strings.TrimSpace(r.Provider.APIKey()) == "" {
			r.emit(Event{Type: EventError, Text: ErrMissingAPIKey.Error()})
			runErr = ErrMissingAPIKey
			break
		}

		iteration++
		out.Iterations = iteration
		r.emit(Event{Type: EventTurnStart, Iteration: iteration})
		r.emit(Event{Type: EventLLMStart, Text: llmSpinnerText()})

		msgs, prepareErr := ctx.PrepareForAPIContext(runCtx, r.RequestOptions)
		if prepareErr != nil {
			r.emit(Event{Type: EventLLMEnd, Iteration: iteration, ContextTokens: ctx.TotalTokens(), MaxContext: r.MaxContext})
			runErr = prepareErr
			break
		}
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

		out.Content = resp.Content
		out.FinishReason = resp.FinishReason
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

		if resp.HasToolCalls {
			completed := false
			for _, tc := range resp.ToolCalls {
				// 已结束或取消时补齐剩余调用的结果，不执行其副作用。
				if completed || runCtx.Err() != nil {
					ctx.AppendTool(tc.ID, tc.Name, "error: tool skipped because the run ended")
					continue
				}
				logx.Debugf("tool call: %s", tc.Name)
				result, done := r.executeTool(runCtx, iteration, tc)
				ctx.AppendTool(tc.ID, tc.Name, result)
				if done {
					completed = true
					out.StopReason = StopCompleted
					out.Output = result
					if text, ok := result.(string); ok {
						out.Content = text
					}
				}
			}
			if err := runCtx.Err(); err != nil {
				runErr = err
				break
			}
			if completed {
				break
			}
		} else {
			logx.Debugf("loop exit: finish_reason=%q no tool calls", resp.FinishReason)
			if resp.FinishReason == "stop" {
				out.StopReason = StopModel
			} else {
				runErr = fmt.Errorf("model ended without completing the turn: finish_reason=%q", resp.FinishReason)
			}
			break
		}
		if r.MaxIter > 0 && iteration >= r.MaxIter {
			out.StopReason = StopMaxIterations
			runErr = ErrMaxIterations
			break
		}
	}

	if runErr == nil {
		runErr = runCtx.Err()
	}
	return out, runErr
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
