package rag

import (
	"context"
	"fmt"
	"strings"
)

// Prefetch 执行一次检索并返回可 InjectUser 的文本（含前缀）。
// hits 为空时返回 ("", nil, nil)，调用方可跳过注入。
func Prefetch(ctx context.Context, r Retriever, q Query) (text string, hits []Hit, err error) {
	if r == nil {
		return "", nil, fmt.Errorf("rag: Prefetch requires Retriever")
	}
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" {
		return "", nil, nil
	}
	q = NormalizeQuery(q, 5)
	hits, err = r.Search(ctx, q)
	if err != nil {
		return "", nil, err
	}
	if len(hits) == 0 {
		return "", hits, nil
	}
	return PrefetchPrefix + FormatHits(hits), hits, nil
}
