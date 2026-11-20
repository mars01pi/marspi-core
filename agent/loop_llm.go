package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/mars/marspi-core/llm"
	"github.com/mars/marspi-core/logx"
)

// callLLM 发起一次模型请求（流式或非流式），并 emit 对应 message 事件。
func (r *Runner) callLLM(runCtx context.Context, msgs []llm.Message, tools []map[string]any, iteration int) (llm.Response, error) {
	timeout, retries := r.RequestOptions.Limits()
	runCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()
	r.emit(Event{Type: EventMessageStart, Iteration: iteration})

	var resp llm.Response
	var err error
	streamed := false

	if r.Stream {
		resp, streamed, err = r.requestStream(runCtx, msgs, tools, iteration)
		if err != nil && retries > 0 && runCtx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logx.Debugf("stream failed, fallback blocking: %v", err)
			resp, err = r.requestBlocking(runCtx, msgs, tools, retries-1)
			streamed = false
		}
	} else {
		resp, err = r.requestBlocking(runCtx, msgs, tools, retries)
	}

	if err != nil {
		return resp, err
	}
	if resp.Model == "" {
		resp.Model = r.Provider.Model()
	}

	r.emit(Event{
		Type:         EventMessageEnd,
		Iteration:    iteration,
		Content:      resp.Content,
		Reasoning:    resp.ReasoningContent,
		FinishReason: resp.FinishReason,
		HasToolCalls: resp.HasToolCalls,
		Streamed:     streamed,
	})
	return resp, nil
}

func (r *Runner) requestBlocking(runCtx context.Context, msgs []llm.Message, tools []map[string]any, retries int) (llm.Response, error) {
	timeout, _ := r.RequestOptions.Limits()
	raw, err := llm.RequestContext(runCtx, r.Provider.APIURL(), r.Provider.BuildBody(msgs, tools),
		r.Provider.Headers(), timeout, retries)
	if err != nil {
		return llm.Response{}, err
	}
	return r.Provider.ParseResponse(raw), nil
}

func (r *Runner) requestStream(runCtx context.Context, msgs []llm.Message, tools []map[string]any, iteration int) (llm.Response, bool, error) {
	timeout, _ := r.RequestOptions.Limits()
	streamed := false
	var reasoning, content strings.Builder
	resp, err := llm.StreamChat(runCtx, r.Provider.APIURL(), r.Provider.BuildBody(msgs, tools),
		r.Provider.Headers(), timeout, func(ch llm.StreamChunk) error {
			if ch.Done {
				return nil
			}
			if ch.ReasoningDelta != "" {
				streamed = true
				reasoning.WriteString(ch.ReasoningDelta)
				r.emit(Event{Type: EventMessageDelta, Iteration: iteration, DeltaField: DeltaReasoning, Delta: reasoning.String()})
			}
			if ch.ContentDelta != "" {
				streamed = true
				content.WriteString(ch.ContentDelta)
				r.emit(Event{Type: EventMessageDelta, Iteration: iteration, DeltaField: DeltaContent, Delta: content.String()})
			}
			return runCtx.Err()
		})
	return resp, streamed, err
}
