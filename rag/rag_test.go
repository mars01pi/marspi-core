package rag_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mars/marspi-core/rag"
)

func TestFormatHits(t *testing.T) {
	got := rag.FormatHits([]rag.Hit{
		{ID: "r1", Title: "Cancel fee", Content: "Driver no-show → passenger free cancel.", Source: "sop/cancel.md", Score: 0.91},
	})
	if !strings.Contains(got, "id=r1") || !strings.Contains(got, "source=sop/cancel.md") {
		t.Fatalf("citation missing: %q", got)
	}
	if !strings.Contains(got, "Driver no-show") {
		t.Fatalf("content missing: %q", got)
	}
}

func TestMockRetriever(t *testing.T) {
	m := &rag.MockRetriever{Docs: []rag.Hit{
		{ID: "1", Content: "取消责任归属规则", Meta: map[string]string{"kb": "rules"}},
		{ID: "2", Content: "费用退款上限", Meta: map[string]string{"kb": "fees"}},
	}}
	hits, err := m.Search(context.Background(), rag.Query{Text: "取消 责任", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "1" {
		t.Fatalf("got %+v", hits)
	}
	hits, err = m.Search(context.Background(), rag.Query{Text: "退款", KB: "fees", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "2" {
		t.Fatalf("kb filter got %+v", hits)
	}
}

func TestHTTPRetriever(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing auth")
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["query"] != "绕路" {
			t.Errorf("query=%v", req["query"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits": []map[string]any{
				{"id": "d1", "content": "绕路判定标准", "source": "rules/detour.md", "score": 0.8},
			},
		})
	}))
	defer srv.Close()

	ret := rag.NewHTTPRetriever(srv.URL, "secret", 0)
	hits, err := ret.Search(context.Background(), rag.Query{Text: "绕路", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "d1" {
		t.Fatalf("got %+v", hits)
	}
}

func TestPrefetch(t *testing.T) {
	m := &rag.MockRetriever{Docs: []rag.Hit{{ID: "x", Content: "SOP 内容关于取消"}}}
	text, hits, err := rag.Prefetch(context.Background(), m, rag.Query{Text: "取消"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits=%d", len(hits))
	}
	if !strings.HasPrefix(text, rag.PrefetchPrefix) {
		t.Fatalf("prefix missing: %q", text)
	}
	text, hits, err = rag.Prefetch(context.Background(), m, rag.Query{Text: "不存在的词xyzabc"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "" || len(hits) != 0 {
		t.Fatalf("expected empty prefetch, text=%q hits=%d", text, len(hits))
	}
}
