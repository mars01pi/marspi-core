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
	if tc.ArgumentsError != "" {
		return errors.New(tc.ArgumentsError)
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

// executeTool 校验、执行单个工具并广播事件；completed 由成功结果的 EndTurn 决定。
func (r *Runner) executeTool(runCtx context.Context, iteration int, tc llm.ToolCall) (result any, completed bool) {
	preview := ""
	validationErr := validateToolCall(tc)
	if validationErr == nil && r.Registry != nil {
		if t, ok := r.Registry.Get(tc.Name); ok {
			preview = safeToolPreview(t, tc.Arguments)
		}
	}

	r.emit(Event{
		Type:        EventToolStart,
		Iteration:   iteration,
		ToolName:    tc.Name,
		ToolCallID:  tc.ID,
		ToolArgs:    tc.Arguments,
		ToolPreview: preview,
	})

	if validationErr != nil || r.Registry == nil {
		errResult := "error: no tool registry configured"
		if validationErr != nil {
			errResult = "error: invalid tool arguments: " + validationErr.Error()
		}
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
	ok := runMeta.Success && !runMeta.Denied && !runMeta.Canceled

	r.emit(Event{
		Type:            EventToolEnd,
		Iteration:       iteration,
		ToolName:        tc.Name,
		ToolCallID:      tc.ID,
		ToolOK:          ok,
		ToolDenied:      runMeta.Denied,
		ToolResultLines: runMeta.PreviewLines,
	})

	if ok && runMeta.EndTurn {
		if s, ok := content.(string); ok && s != "" {
			r.emit(Event{Type: EventMessageEnd, Content: s})
		}
		return content, true
	}
	return content, false
}

func safeToolPreview(t tool.Tool, args map[string]any) (preview string) {
	// 预览不应阻止工具自行校验缺失或类型错误的参数。
	defer func() { _ = recover() }()
	return t.Preview(args)
}
