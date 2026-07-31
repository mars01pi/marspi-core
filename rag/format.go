package rag

import (
	"fmt"
	"strings"
)

// FormatHits 将命中格式化为可注入上下文或工具回填的文本（含 citation）。
func FormatHits(hits []Hit) string {
	if len(hits) == 0 {
		return "No knowledge hits."
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		cite := citation(h)
		b.WriteString(fmt.Sprintf("[%d] %s\n", i+1, cite))
		if title := strings.TrimSpace(h.Title); title != "" && title != h.ID {
			b.WriteString("title: ")
			b.WriteString(title)
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimSpace(h.Content))
	}
	return b.String()
}

func citation(h Hit) string {
	parts := make([]string, 0, 3)
	if id := strings.TrimSpace(h.ID); id != "" {
		parts = append(parts, "id="+id)
	}
	if src := strings.TrimSpace(h.Source); src != "" {
		parts = append(parts, "source="+src)
	}
	if h.Score != 0 {
		parts = append(parts, fmt.Sprintf("score=%.3f", h.Score))
	}
	if len(parts) == 0 {
		return "source=unknown"
	}
	return strings.Join(parts, " | ")
}

// PrefetchPrefix 是 InjectUser 时建议使用的前缀，便于模型识别检索上下文。
const PrefetchPrefix = "[Retrieved knowledge — cite these sources when answering]\n\n"
