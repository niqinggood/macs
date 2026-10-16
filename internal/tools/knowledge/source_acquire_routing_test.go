package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceAcquireRoutesDescriptionVerbatimAndFreezesSource(t *testing.T) {
	const description = "chatroom 有一篇星河缓存转型帖子"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/nova/chatroom/topics":
			fmt.Fprint(w, `{"topics":[{"id":18,"postCount":1}]}`)
		case "/nova/chatroom/topics/18/posts":
			fmt.Fprint(w, `{"posts":[{"id":1801,"topicId":18,"content":"# 星河缓存转型复盘\n列表预览"}]}`)
		case "/nova/chatroom/posts/1801":
			fmt.Fprint(w, `{"post":{"id":1801,"topicId":18,"content":"# 星河缓存转型复盘\n完整正文与行动建议。"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	tool := NewSourceAcquireTool(workspace, newTestNovaChatroomSearchTool(workspace, server))
	output := filepath.Join(".openkitty", "storage", ".system", "source-router", "source.json")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"description": description,
		"max_results": 1,
		"max_pages":   2,
		"output_path": output,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var receipt NovaChatroomSearchReceipt
	if err := json.Unmarshal([]byte(result), &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.Query != description || receipt.SelectedPostID != 1801 {
		t.Fatalf("router changed description or selected wrong source: %+v", receipt)
	}
	bundleBytes, err := os.ReadFile(filepath.Join(workspace, output))
	if err != nil {
		t.Fatalf("read frozen source: %v", err)
	}
	var bundle NovaChatroomSourceBundle
	if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if bundle.Query != description || len(bundle.Matches) != 1 || !strings.Contains(bundle.Matches[0].Content, "完整正文") {
		t.Fatalf("unexpected frozen bundle: %+v", bundle)
	}
}
