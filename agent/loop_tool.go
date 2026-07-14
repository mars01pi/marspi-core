package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mars/marspi-core/llm"
	"github.com/mars/marspi-core/tool"
)

func validateToolCall(tc llm.ToolCall) error {
	if strings.TrimSpace(tc.Name) == "" {
		return errors.New("empty tool name")
	}
	if tc.Arguments == nil {
		return nil
	}
	raw, err := json.Marshal(tc.Arguments)
	if err != nil {
		return fmt.Errorf("marshal arguments: %w", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("invalid arguments json: %w", err)
	}
	return nil
}

// executeTool 校验、执行单个 tool call 并 emit tool_* 事件；completed 表示 attempt_completion 结束 loop。
func (r *Runner) executeTool(runCtx context.Context, iteration int, tc llm.ToolCall) (result any, completed bool) {
	preview := ""
	if t, ok := r.Registry.Get(tc.Name); ok {
		preview = t.Preview(tc.Arguments)
	}

	r.emit(Event{
		Type:        EventToolStart,
		Iteration:   iteration,
		ToolName:    tc.Name,
		ToolCallID:  tc.ID,
		ToolArgs:    tc.Arguments,
		ToolPreview: preview,
	})

	if err := validateToolCall(tc); err != nil {
		errResult := "error: invalid tool arguments: " + err.Error()
		r.emit(Event{
			Type:            EventToolEnd,
			Iteration:       iteration,
			ToolName:        tc.Name,
			ToolCallID:      tc.ID,
			ToolOK:          false,
			ToolResultLines: []string{errResult},
		})
		return errResult, false
	}

	meta := tool.ExecutionMeta{ToolCallID: tc.ID}
	if r.ToolMeta != nil {
		meta = r.ToolMeta(runCtx, tc.Name, tc.ID)
		if meta.ToolCallID == "" {
			meta.ToolCallID = tc.ID
		}
	}

	content, runMeta := r.Registry.ExecuteQuietCtx(runCtx, meta, tc.Name, tc.Arguments)
	ok := runMeta.Success && !runMeta.Denied && !runMeta.Canceled && toolResultOK(content)

	r.emit(Event{
		Type:            EventToolEnd,
		Iteration:       iteration,
		ToolName:        tc.Name,
		ToolCallID:      tc.ID,
		ToolOK:          ok,
		ToolDenied:      runMeta.Denied,
		ToolResultLines: runMeta.PreviewLines,
	})

	if tc.Name == "attempt_completion" {
		if s, ok := content.(string); ok && s != "" {
			r.emit(Event{Type: EventMessageEnd, Content: s})
		}
		return content, true
	}
	return content, false
}
