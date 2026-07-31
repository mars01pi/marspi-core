// Package rag 定义知识库检索契约与适配器（HTTP / Mock），不含向量索引实现。
package rag

import "context"

// Hit 是一条检索命中，对齐 LangChain Document 的最小可用字段。
type Hit struct {
	ID      string            `json:"id,omitempty"`
	Title   string            `json:"title,omitempty"`
	Content string            `json:"content"`
	Source  string            `json:"source,omitempty"` // 文件路径 / URL / doc_id
	Score   float64           `json:"score,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// Query 是一次检索请求。
type Query struct {
	Text    string            `json:"query"`
	TopK    int               `json:"top_k,omitempty"`
	KB      string            `json:"kb,omitempty"` // 可选知识库名
	Filters map[string]string `json:"filters,omitempty"`
}

// Retriever 将非结构化 query 转为文档片段。索引构建由平台侧负责。
type Retriever interface {
	Search(ctx context.Context, q Query) ([]Hit, error)
}

// NormalizeQuery 填充默认 TopK，并 trim 文本。
func NormalizeQuery(q Query, defaultTopK int) Query {
	if defaultTopK <= 0 {
		defaultTopK = 5
	}
	if q.TopK <= 0 {
		q.TopK = defaultTopK
	}
	if q.TopK > 50 {
		q.TopK = 50
	}
	return q
}
