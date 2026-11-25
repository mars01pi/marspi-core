package tool

import (
	"context"
	"testing"
)

type schemaTool struct {
	stubTool
	schema map[string]any
}

func (s schemaTool) InputSchema() map[string]any { return s.schema }

func TestEmptyRegistryAndNestedSchema(t *testing.T) {
	reg := NewEmptyRegistry(nil)
	if len(reg.Names()) != 0 || len(reg.Schemas()) != 0 {
		t.Fatal("empty registry exposed builtins")
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"changes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "integer", "minimum": 0}},
					"required":   []string{"value"}, "additionalProperties": false,
				},
			},
		},
		"required": []string{"changes"}, "additionalProperties": false,
	}
	reg.Register(schemaTool{stubTool: stubTool{name: "batch"}, schema: schema})
	params := reg.Schemas()[0]["function"].(map[string]any)["parameters"].(map[string]any)
	changes := params["properties"].(map[string]any)["changes"].(map[string]any)
	items := changes["items"].(map[string]any)
	if changes["type"] != "array" || items["additionalProperties"] != false {
		t.Fatalf("lost nested schema: %#v", params)
	}
	items["type"] = "string"
	original := schema["properties"].(map[string]any)["changes"].(map[string]any)["items"].(map[string]any)
	if original["type"] != "object" {
		t.Fatal("returned schema mutated tool definition")
	}
}

func TestInvalidInputSchema(t *testing.T) {
	for _, schema := range []map[string]any{nil, {"type": "array"}, {"type": "object", "bad": make(chan int)}} {
		reg := NewEmptyRegistry(nil)
		reg.Register(schemaTool{stubTool: stubTool{name: "bad"}, schema: schema})
		if _, err := reg.SchemasChecked(); err == nil {
			t.Fatalf("accepted invalid schema: %#v", schema)
		}
	}
}

func TestCompletionToolValidation(t *testing.T) {
	reg := NewEmptyRegistry(nil)
	reg.Register(NewAttemptCompletionTool())
	for _, value := range []any{nil, "", "  ", 42, "Which option?"} {
		content, meta := reg.ExecuteQuiet("attempt_completion", map[string]any{"result": value})
		wantOK := value == "Which option?"
		if meta.Success != wantOK || meta.EndTurn != wantOK {
			t.Fatalf("value=%v content=%v meta=%+v", value, content, meta)
		}
	}
}

type terminalTool struct {
	Base
	deny   bool
	cancel context.CancelFunc
	result Result
	panic  bool
}

func (t terminalTool) Name() string                { return "terminal" }
func (t terminalTool) Description() string         { return "terminal" }
func (t terminalTool) Params() []Param             { return nil }
func (t terminalTool) Confirm(map[string]any) bool { return !t.deny }
func (t terminalTool) Run(map[string]any) Result {
	if t.cancel != nil {
		t.cancel()
	}
	if t.panic {
		panic("tool failure")
	}
	return t.result
}

func TestEndTurnRequiresSuccessfulExecution(t *testing.T) {
	for _, name := range []string{"success", "failure", "denied", "canceled", "panic"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tl := terminalTool{result: Result{Success: true, Content: "answer", EndTurn: true}}
			switch name {
			case "failure":
				tl.result.Success = false
			case "denied":
				tl.deny = true
			case "canceled":
				tl.cancel = cancel
			case "panic":
				tl.panic = true
			}
			reg := NewEmptyRegistry(nil)
			reg.Register(tl)
			content, meta := reg.ExecuteQuietCtx(ctx, ExecutionMeta{}, tl.Name(), nil)
			if meta.EndTurn != (name == "success") || meta.Success != (name == "success") {
				t.Fatalf("content=%v meta=%+v", content, meta)
			}
			if name == "canceled" && !meta.Canceled {
				t.Fatal("lost cancellation")
			}
		})
	}
}
