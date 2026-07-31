package tool

import (
	"strings"
	"testing"

	"github.com/mars/marspi-core/config"
	"github.com/mars/marspi-core/console"
	"github.com/mars/marspi-core/rag"
)

func TestRegisterKnowledge(t *testing.T) {
	cfg := &config.Config{ProjectRoot: t.TempDir()}
	reg := NewRegistry(cfg, console.Nop{}, nil, nil)
	if _, ok := reg.Get("search_knowledge"); ok {
		t.Fatal("should not register without retriever")
	}
	reg.RegisterKnowledge(&rag.MockRetriever{Docs: []rag.Hit{
		{ID: "1", Content: "取消费用规则细则", Source: "sop.md"},
	}}, 5)
	ttool, ok := reg.Get("search_knowledge")
	if !ok {
		t.Fatal("missing search_knowledge")
	}
	res := ttool.Run(map[string]any{"query": "取消"})
	if !res.Success {
		t.Fatalf("fail: %v", res.Content)
	}
	s, _ := res.Content.(string)
	if !strings.Contains(s, "id=1") || !strings.Contains(s, "取消费用") {
		t.Fatalf("bad result: %q", s)
	}
}

func TestNewRegistryAutoHTTPFromConfig(t *testing.T) {
	cfg := &config.Config{ProjectRoot: t.TempDir(), RAGURL: "http://example.invalid/rag", RAGTopK: 3}
	reg := NewRegistry(cfg, console.Nop{}, nil, nil)
	if _, ok := reg.Get("search_knowledge"); !ok {
		t.Fatal("expected auto-register from RAGURL")
	}
}
