package tool

import (
	"context"
	"strings"
	"testing"
)

type stubProvider struct {
	tools []Tool
}

func (s stubProvider) ID() string { return "stub" }
func (s stubProvider) Tools() []Tool {
	return s.tools
}
func (s stubProvider) Refresh(context.Context) error { return nil }
func (s stubProvider) Close() error                  { return nil }

type stubTool struct {
	Base
	name   string
	lines  int
	width  int
	output string
}

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "stub" }
func (s stubTool) Params() []Param {
	return nil
}
func (s stubTool) PreviewLines() int         { return s.lines }
func (s stubTool) PreviewWidth() int         { return s.width }
func (s stubTool) Run(map[string]any) Result { return OK(s.output) }

func TestFormatResultPreviewTruncates(t *testing.T) {
	st := stubTool{name: "t", lines: 2, width: 10, output: "line1\nline2\nline3"}
	lines := FormatResultPreview(st, st.output)
	if len(lines) != 3 {
		t.Fatalf("lines: %v", lines)
	}
	if !strings.HasPrefix(lines[2], "... and 1 more line") {
		t.Fatalf("suffix: %q", lines[2])
	}
}

func TestExecuteQuietUnknownTool(t *testing.T) {
	reg := NewEmptyRegistry(nil)
	content, meta := reg.ExecuteQuiet("missing", nil)
	if meta.Success {
		t.Fatal("expected failure")
	}
	if _, ok := content.(string); !ok {
		t.Fatalf("content type: %T", content)
	}
}

func TestExecuteQuietCtxCanceled(t *testing.T) {
	reg := NewEmptyRegistry(nil)
	reg.Register(stubTool{name: "echo", lines: 2, width: 20, output: "hi"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	content, meta := reg.ExecuteQuietCtx(ctx, ExecutionMeta{}, "echo", nil)
	if !meta.Canceled || meta.Success {
		t.Fatalf("meta=%+v content=%v", meta, content)
	}
}

type ctxAwareTool struct {
	Base
	gotMeta ExecutionMeta
	sawCtx  bool
}

func (c *ctxAwareTool) Name() string        { return "ctx_tool" }
func (c *ctxAwareTool) Description() string { return "ctx" }
func (c *ctxAwareTool) Params() []Param     { return nil }
func (c *ctxAwareTool) Run(map[string]any) Result {
	return Fail("Run should not be called")
}
func (c *ctxAwareTool) RunCtx(ctx context.Context, meta ExecutionMeta, _ map[string]any) Result {
	c.gotMeta = meta
	c.sawCtx = ctx != nil
	return OK("ctx-ok")
}

func TestExecuteQuietCtxUsesContextualTool(t *testing.T) {
	reg := NewEmptyRegistry(nil)
	ct := &ctxAwareTool{}
	reg.Register(ct)
	em := ExecutionMeta{ThreadID: "t1", LeaseEpoch: 7, IdempotencyKey: "abc"}
	content, meta := reg.ExecuteQuietCtx(context.Background(), em, "ctx_tool", nil)
	if !meta.Success || content != "ctx-ok" {
		t.Fatalf("content=%v meta=%+v", content, meta)
	}
	if !ct.sawCtx || ct.gotMeta.ThreadID != "t1" || ct.gotMeta.LeaseEpoch != 7 {
		t.Fatalf("got meta %+v", ct.gotMeta)
	}
}

func TestRegistryAddProviderAddsTools(t *testing.T) {
	reg := NewEmptyRegistry(nil)
	err := reg.AddProvider(stubProvider{tools: []Tool{stubTool{name: "prov", lines: 2, width: 20, output: "ok"}}})
	if err != nil {
		t.Fatalf("AddProvider error: %v", err)
	}
	if _, ok := reg.Get("prov"); !ok {
		t.Fatalf("provider tool not found")
	}
}
