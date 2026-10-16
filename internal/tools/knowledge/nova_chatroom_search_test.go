package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNovaChatroomSearchToolFreezesFullDetailAndReturnsFinalFileHash(t *testing.T) {
	const (
		selectedID    = int64(731)
		preview       = "# Acme latency retrospective\ntruncated list preview"
		fullPost      = "# Acme latency retrospective\n\nThis is the complete post detail, with causes, evidence, and remediation."
		selectedStamp = "2026-06-18T10:30:00Z"
	)
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/nova/chatroom/topics":
			fmt.Fprint(w, `{"topics":[{"id":11,"postCount":100},{"id":12,"postCount":100}]}`)
		case "/nova/chatroom/topics/11/posts", "/nova/chatroom/topics/12/posts":
			if r.URL.Query().Get("limit") != "50" {
				t.Errorf("page size = %q, want 50", r.URL.Query().Get("limit"))
			}
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, `{"posts":[{"id":700,"topicId":11,"content":"unrelated release notes"}]}`)
				return
			}
			if r.URL.Path == "/nova/chatroom/topics/11/posts" {
				fmt.Fprintf(w, `{"posts":[{"id":%d,"topicId":11,"author":"research-bot","createdAt":%q,"contentType":"markdown","content":%q}]}`, selectedID, selectedStamp, preview)
				return
			}
			fmt.Fprint(w, `{"posts":[]}`)
		case "/nova/chatroom/posts/731":
			fmt.Fprintf(w, `{"post":{"id":%d,"topicId":11,"author":"research-bot","createdAt":%q,"contentType":"markdown","content":%q}}`, selectedID, selectedStamp, fullPost)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	tool := newTestNovaChatroomSearchTool(workspace, server)
	tool.now = func() time.Time { return time.Date(2026, 7, 11, 1, 2, 3, 4, time.UTC) }
	output := filepath.Join(".openkitty", "storage", ".system", "pipeline_sources", "run-a", "source.json")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":       "Acme latency",
		"max_results": float64(3),
		"max_pages":   float64(4),
		"output_path": output,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var receipt novaChatroomSearchReceipt
	if err := json.Unmarshal([]byte(result), &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, result)
	}
	if !receipt.OK || receipt.Source != "nova_chatroom" || receipt.MatchCount != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if receipt.Selected.PostID != selectedID || receipt.SelectedPostID != selectedID {
		t.Fatalf("selected IDs = %d/%d, want %d", receipt.Selected.PostID, receipt.SelectedPostID, selectedID)
	}
	if receipt.Selected.SourceURL != server.URL+"/nova/chatroom/posts/731" {
		t.Fatalf("selected source URL = %q", receipt.Selected.SourceURL)
	}
	contentHash := sha256.Sum256([]byte(fullPost))
	if receipt.Selected.ContentSHA256 != hex.EncodeToString(contentHash[:]) {
		t.Fatalf("selected content hash = %q", receipt.Selected.ContentSHA256)
	}

	absOutput := filepath.Join(workspace, output)
	fileBytes, err := os.ReadFile(absOutput)
	if err != nil {
		t.Fatalf("read frozen source: %v", err)
	}
	fileHash := sha256.Sum256(fileBytes)
	fileHashHex := hex.EncodeToString(fileHash[:])
	if receipt.WholeFileSHA256 != fileHashHex || receipt.FileSHA256 != fileHashHex {
		t.Fatalf("receipt hashes = %q/%q, final file = %q", receipt.WholeFileSHA256, receipt.FileSHA256, fileHashHex)
	}
	if receipt.FileBytes != len(fileBytes) {
		t.Fatalf("file_bytes = %d, want %d", receipt.FileBytes, len(fileBytes))
	}
	var bundle novaChatroomSourceBundle
	if err := json.Unmarshal(fileBytes, &bundle); err != nil {
		t.Fatalf("decode source bundle: %v", err)
	}
	if bundle.MatchCount != 1 || len(bundle.Matches) != 1 || bundle.Matches[0].Content != fullPost {
		t.Fatalf("bundle did not freeze full detail: %+v", bundle)
	}
	if bundle.Matches[0].Content == preview {
		t.Fatal("bundle froze list preview instead of post detail")
	}

	mu.Lock()
	requestLog := strings.Join(requests, "\n")
	mu.Unlock()
	if !strings.Contains(requestLog, "page=1") || !strings.Contains(requestLog, "page=2") {
		t.Fatalf("expected paginated discovery requests:\n%s", requestLog)
	}
	if !strings.Contains(requestLog, "/nova/chatroom/posts/731") {
		t.Fatalf("selected post detail was not fetched:\n%s", requestLog)
	}
}

func TestNovaChatroomSearchToolBoundsProviderConcurrency(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/nova/chatroom/topics" {
			fmt.Fprint(w, `{"topics":[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5},{"id":6}]}`)
			return
		}
		if strings.Contains(r.URL.Path, "/topics/") && strings.HasSuffix(r.URL.Path, "/posts") {
			current := active.Add(1)
			for {
				old := maximum.Load()
				if current <= old || maximum.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(35 * time.Millisecond)
			active.Add(-1)
			fmt.Fprint(w, `{"posts":[{"id":99,"content":"unrelated"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	workspace := t.TempDir()
	tool := newTestNovaChatroomSearchTool(workspace, server)
	tool.maxConcurrency = 2
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":       "target signal",
		"max_pages":   1,
		"output_path": filepath.Join(".openkitty", "storage", "run-b", "source.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "no Nova chatroom posts matched") {
		t.Fatalf("Execute() error = %v, want no-match error", err)
	}
	if got := maximum.Load(); got > 2 {
		t.Fatalf("provider concurrency = %d, want <= 2", got)
	} else if got < 2 {
		t.Fatalf("provider concurrency = %d, test did not observe bounded parallel reads", got)
	}
}

func TestNovaChatroomSearchToolRanksHighestSemanticMatchFirst(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/nova/chatroom/topics":
			fmt.Fprint(w, `{"topics":[{"id":8,"postCount":2}]}`)
		case "/nova/chatroom/topics/8/posts":
			fmt.Fprint(w, `{"posts":[`+
				`{"id":802,"topicId":8,"createdAt":"2026-07-11T12:00:00Z","content":"# Weekly update\norbital systems and cache operations were discussed"},`+
				`{"id":801,"topicId":8,"createdAt":"2026-07-10T12:00:00Z","content":"# Orbital cache design\npreview"}`+
				`]}`)
		case "/nova/chatroom/posts/801":
			fmt.Fprint(w, `{"post":{"id":801,"topicId":8,"createdAt":"2026-07-10T12:00:00Z","content":"# Orbital cache design\nfull high-confidence detail"}}`)
		case "/nova/chatroom/posts/802":
			fmt.Fprint(w, `{"post":{"id":802,"topicId":8,"createdAt":"2026-07-11T12:00:00Z","content":"# Weekly update\norbital systems and cache operations were discussed in full"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	tool := newTestNovaChatroomSearchTool(workspace, server)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":       "orbital cache",
		"max_results": 2,
		"max_pages":   1,
		"output_path": filepath.Join(".openkitty", "storage", "run-rank", "source.json"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var receipt novaChatroomSearchReceipt
	if err := json.Unmarshal([]byte(result), &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.SelectedPostID != 801 {
		t.Fatalf("selected post = %d, want highest semantic score 801", receipt.SelectedPostID)
	}
}

func TestNovaChatroomScoreKeepsDistinctiveVerbatimTitleTerm(t *testing.T) {
	post := novaChatroomPost{Content: "# 星河缓存架构复盘\n完整技术正文"}
	if score := scoreNovaChatroomPost(post, "星河缓存 竞争对手 行业衰落"); score < 200 {
		t.Fatalf("distinctive verbatim title term should survive extra guessed terms, score=%d", score)
	}
	if score := scoreNovaChatroomPost(post, "星河 行业 衰落"); score != 0 {
		t.Fatalf("weak partial terms must not select a source, score=%d", score)
	}
}

func TestNovaChatroomSearchToolHonorsTotalDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/nova/chatroom/topics" {
			fmt.Fprint(w, `{"topics":[{"id":41}]}`)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	workspace := t.TempDir()
	tool := newTestNovaChatroomSearchTool(workspace, server)
	tool.totalTimeout = 90 * time.Millisecond
	tool.requestTimeout = 2 * time.Second
	started := time.Now()
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":       "deadline subject",
		"output_path": filepath.Join(".openkitty", "storage", "run-c", "source.json"),
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("Execute() error = %v, want deadline error", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deadline took %v, want < 1s", elapsed)
	}
}

func TestNovaChatroomSearchToolRejectsOutputOutsideStorageBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	workspace := t.TempDir()
	tool := newTestNovaChatroomSearchTool(workspace, server)
	outside := filepath.Join(t.TempDir(), "source.json")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":       "valid subject",
		"output_path": outside,
	})
	if err == nil || !strings.Contains(err.Error(), "under workspace/.openkitty/storage") {
		t.Fatalf("Execute() error = %v, want storage-boundary error", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("network requests = %d, want 0", requests.Load())
	}
}

func TestNovaChatroomSearchToolRegistrationTraits(t *testing.T) {
	tool := NewNovaChatroomSearchTool(t.TempDir())
	if tool.Name() != "nova_chatroom_search" || !tool.IsReadOnly() || !tool.IsNeverParallel() {
		t.Fatalf("unexpected tool traits: name=%q readOnly=%v neverParallel=%v", tool.Name(), tool.IsReadOnly(), tool.IsNeverParallel())
	}
}

func newTestNovaChatroomSearchTool(workspace string, server *httptest.Server) *NovaChatroomSearchTool {
	tool := NewNovaChatroomSearchTool(workspace)
	tool.apiBaseURL = server.URL + "/nova/chatroom"
	tool.canonicalBaseURL = server.URL + "/nova/chatroom"
	tool.httpClient = server.Client()
	tool.requestTimeout = time.Second
	tool.totalTimeout = 3 * time.Second
	return tool
}
