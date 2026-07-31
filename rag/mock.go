package rag

import (
	"context"
	"strings"
)

// MockRetriever 用内存文档做关键词打分，便于单测与无平台时本地演示。
type MockRetriever struct {
	Docs []Hit
}

// Search 对 Content/Title/Source 做简单子串命中打分。
func (m *MockRetriever) Search(_ context.Context, q Query) ([]Hit, error) {
	q = NormalizeQuery(q, 5)
	text := strings.ToLower(strings.TrimSpace(q.Text))
	if text == "" {
		return nil, nil
	}
	keywords := strings.Fields(text)
	if len(keywords) == 0 {
		keywords = []string{text}
	}

	type scored struct {
		hit   Hit
		score float64
	}
	var out []scored
	for _, doc := range m.Docs {
		if q.KB != "" && doc.Meta != nil {
			if kb := doc.Meta["kb"]; kb != "" && kb != q.KB {
				continue
			}
		}
		blob := strings.ToLower(doc.Title + " " + doc.Content + " " + doc.Source + " " + doc.ID)
		score := 0.0
		for _, kw := range keywords {
			if kw == "" {
				continue
			}
			if strings.Contains(blob, kw) {
				score += float64(strings.Count(blob, kw)) * 10
			}
		}
		if score <= 0 {
			continue
		}
		h := doc
		h.Score = score
		out = append(out, scored{hit: h, score: score})
	}
	// 简单选择排序取 TopK
	for i := 0; i < len(out); i++ {
		best := i
		for j := i + 1; j < len(out); j++ {
			if out[j].score > out[best].score {
				best = j
			}
		}
		out[i], out[best] = out[best], out[i]
	}
	if len(out) > q.TopK {
		out = out[:q.TopK]
	}
	hits := make([]Hit, len(out))
	for i := range out {
		hits[i] = out[i].hit
	}
	return hits, nil
}
