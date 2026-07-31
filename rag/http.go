package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPRetriever 调用平台检索 API。
//
// 请求: POST {URL}  JSON {"query","top_k","kb","filters"}
// 响应: {"hits":[{"id","title","content","source","score","meta"}]}
// 亦兼容顶层数组 [{"id",...}]。
type HTTPRetriever struct {
	URL     string
	APIKey  string
	Client  *http.Client
	Timeout time.Duration
}

// NewHTTPRetriever 构建 HTTP 检索器；timeout<=0 时默认 30s。
func NewHTTPRetriever(url, apiKey string, timeout time.Duration) *HTTPRetriever {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPRetriever{
		URL:     strings.TrimSpace(url),
		APIKey:  strings.TrimSpace(apiKey),
		Client:  &http.Client{Timeout: timeout},
		Timeout: timeout,
	}
}

type httpRequestBody struct {
	Query   string            `json:"query"`
	TopK    int               `json:"top_k,omitempty"`
	KB      string            `json:"kb,omitempty"`
	Filters map[string]string `json:"filters,omitempty"`
}

type httpResponseBody struct {
	Hits []Hit `json:"hits"`
}

func (h *HTTPRetriever) Search(ctx context.Context, q Query) ([]Hit, error) {
	if h == nil || h.URL == "" {
		return nil, fmt.Errorf("rag: HTTPRetriever URL is empty")
	}
	q = NormalizeQuery(q, 5)
	if strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}

	body, err := json.Marshal(httpRequestBody{
		Query:   q.Text,
		TopK:    q.TopK,
		KB:      q.KB,
		Filters: q.Filters,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.APIKey)
	}

	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: h.Timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rag: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var wrapped httpResponseBody
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Hits != nil {
		return wrapped.Hits, nil
	}
	var arr []Hit
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	return nil, fmt.Errorf("rag: unexpected response shape: %s", truncate(string(raw), 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
