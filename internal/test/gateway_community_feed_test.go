package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchNovaCommunityFeedUsesGETAndKeepsTechnologyPosts(t *testing.T) {
	var nonGET atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			nonGET.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/nova/chatroom/topics":
			fmt.Fprint(w, `{"topics":[{"id":1,"postCount":2,"category":"ai"},{"id":2,"postCount":1,"category":"robotics"}]}`)
		case "/nova/chatroom/topics/1/posts":
			fmt.Fprint(w, `{"posts":[`+
				`{"id":101,"topicId":1,"author":"robot-lab","createdAt":"2026-07-22T08:00:00Z","content":"# 机械臂视觉抓取\nROS 与边缘 AI 控制实践"},`+
				`{"id":102,"topicId":1,"author":"cook","createdAt":"2026-07-23T09:00:00Z","content":"# 夏季甜点\n冰淇淋制作记录"}`+
				`]}`)
		case "/nova/chatroom/topics/2/posts":
			fmt.Fprint(w, `{"posts":[{"id":201,"topicId":2,"author":"edge-team","createdAt":"2026-07-23T08:30:00Z","content":"# Edge robot release\nOpen source robotics software update"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tool := newTestNovaChatroomSearchTool(t.TempDir(), server)
	tool.SetCanonicalBaseURL("https://community.example/nova/chatroom")
	posts, err := fetchNovaCommunityFeedWithTool(context.Background(), tool, 8)
	if err != nil {
		t.Fatal(err)
	}
	if nonGET.Load() != 0 {
		t.Fatalf("community feed issued %d non-GET requests", nonGET.Load())
	}
	if len(posts) != 2 {
		t.Fatalf("technology posts = %d, want 2: %#v", len(posts), posts)
	}
	if posts[0].ID != 201 || posts[1].ID != 101 {
		t.Fatalf("posts are not newest first: %#v", posts)
	}
	if posts[0].SourceURL != "https://community.example/nova/chatroom/posts/201" {
		t.Fatalf("source URL = %q", posts[0].SourceURL)
	}
}

func TestCommunityFeedStoreCachesAndFallsBackToStaleContent(t *testing.T) {
	startedAt := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	now := startedAt
	var calls atomic.Int32
	fail := false
	store := newCommunityFeedStore(func(context.Context, int) ([]communityFeedPost, error) {
		calls.Add(1)
		if fail {
			return nil, context.DeadlineExceeded
		}
		return []communityFeedPost{{ID: 7, Title: "Robot update"}}, nil
	})
	store.now = func() time.Time { return now }

	first, stale, err := store.load(context.Background())
	if err != nil || stale || len(first.Posts) != 1 {
		t.Fatalf("first load = %#v, stale=%v, err=%v", first, stale, err)
	}
	if _, _, err := store.load(context.Background()); err != nil || calls.Load() != 1 {
		t.Fatalf("fresh cache was not reused: calls=%d, err=%v", calls.Load(), err)
	}

	now = now.Add(communityFeedFreshFor + time.Second)
	fail = true
	second, stale, err := store.load(context.Background())
	if err != nil || !stale || len(second.Posts) != 1 || second.Posts[0].ID != 7 {
		t.Fatalf("stale fallback = %#v, stale=%v, err=%v", second, stale, err)
	}

	now = startedAt.Add(communityFeedStaleFor + time.Second)
	if _, stale, err := store.load(context.Background()); err == nil || stale {
		t.Fatalf("expired stale cache should fail: stale=%v, err=%v", stale, err)
	}
}

func TestCommunityFeedStoreCoalescesConcurrentRefreshes(t *testing.T) {
	var calls atomic.Int32
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	store := newCommunityFeedStore(func(context.Context, int) ([]communityFeedPost, error) {
		if calls.Add(1) == 1 {
			close(fetchStarted)
		}
		<-releaseFetch
		return []communityFeedPost{{ID: 8, Title: "Concurrent robot update"}}, nil
	})

	const readers = 16
	results := make(chan error, readers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(readers)
	for range readers {
		go func() {
			defer waitGroup.Done()
			entry, stale, err := store.load(context.Background())
			if err == nil && (stale || len(entry.Posts) != 1 || entry.Posts[0].ID != 8) {
				err = fmt.Errorf("unexpected feed entry: %#v, stale=%v", entry, stale)
			}
			results <- err
		}()
	}
	<-fetchStarted
	close(releaseFetch)
	waitGroup.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent refresh calls = %d, want 1", calls.Load())
	}
}

func TestCommunityPostPreviewRemovesDuplicateTitleAndMarkdownNoise(t *testing.T) {
	title, summary := communityPostPreview("# 🦞 🦞 ![🦞](https://example.com/lobster.png) Edge Robot Release\n" +
		"🦞 Edge Robot Release\n" +
		"OpenKitty now supports [ROS 2](https://docs.ros.org/) control. " +
		"![robot photo](https://example.com/robot.png)\n" +
		"Details: https://example.com/release")
	if title != "🦞 Edge Robot Release" {
		t.Fatalf("preview title = %q", title)
	}
	if strings.Contains(summary, "Edge Robot Release") {
		t.Fatalf("preview repeats its title: %q", summary)
	}
	if strings.Contains(summary, "https://") || strings.Contains(summary, "](") {
		t.Fatalf("preview retained Markdown URL noise: %q", summary)
	}
	if !strings.Contains(summary, "ROS 2") || strings.Contains(summary, "robot photo") {
		t.Fatalf("preview did not keep text while removing image noise: %q", summary)
	}
}

func TestCommunityFeedETagCoversVisibleRepresentation(t *testing.T) {
	posts := []communityFeedPost{{ID: 1, Title: "Robot update"}}
	firstFetch := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	freshETag := communityFeedETag(posts, firstFetch, false)
	if freshETag == communityFeedETag(posts, firstFetch.Add(time.Second), false) {
		t.Fatal("ETag did not change with fetched_at")
	}
	if freshETag == communityFeedETag(posts, firstFetch, true) {
		t.Fatal("ETag did not change with stale state")
	}
	if freshETag == communityFeedETag(append(posts, communityFeedPost{ID: 2}), firstFetch, false) {
		t.Fatal("ETag did not change with posts")
	}
}

func TestHandleCommunityFeedIsBoundedGETWithCacheHeaders(t *testing.T) {
	previousStore := defaultCommunityFeedStore
	defaultCommunityFeedStore = newCommunityFeedStore(func(context.Context, int) ([]communityFeedPost, error) {
		return []communityFeedPost{
			{
				ID:        91,
				Title:     "Edge robotics update",
				SourceURL: "https://community.example/posts/91",
			},
			{
				ID:        92,
				Title:     "AI hardware update",
				SourceURL: "https://community.example/posts/92",
			},
		}, nil
	})
	t.Cleanup(func() { defaultCommunityFeedStore = previousStore })

	gateway := &GatewayService{}
	request := httptest.NewRequest(http.MethodGet, "/api/community/feed?limit=1", nil)
	recorder := httptest.NewRecorder()
	gateway.handleCommunityFeed(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("feed status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("ETag") == "" || recorder.Header().Get("Cache-Control") == "" {
		t.Fatalf("feed cache headers missing: %#v", recorder.Header())
	}
	var payload struct {
		OK    bool                `json:"ok"`
		Posts []communityFeedPost `json:"posts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || !payload.OK || len(payload.Posts) != 1 {
		t.Fatalf("feed payload = %#v, err = %v", payload, err)
	}

	twoPostRequest := httptest.NewRequest(http.MethodGet, "/api/community/feed?limit=2", nil)
	twoPostRequest.Header.Set("If-None-Match", recorder.Header().Get("ETag"))
	twoPostRecorder := httptest.NewRecorder()
	gateway.handleCommunityFeed(twoPostRecorder, twoPostRequest)
	if twoPostRecorder.Code != http.StatusOK {
		t.Fatalf("different-limit conditional feed status = %d, want 200", twoPostRecorder.Code)
	}
	if twoPostRecorder.Header().Get("ETag") == recorder.Header().Get("ETag") {
		t.Fatal("different response bodies shared an ETag")
	}

	notModifiedRequest := httptest.NewRequest(http.MethodGet, "/api/community/feed?limit=2", nil)
	notModifiedRequest.Header.Set("If-None-Match", twoPostRecorder.Header().Get("ETag"))
	notModifiedRecorder := httptest.NewRecorder()
	gateway.handleCommunityFeed(notModifiedRecorder, notModifiedRequest)
	if notModifiedRecorder.Code != http.StatusNotModified {
		t.Fatalf("matching conditional feed status = %d, want 304", notModifiedRecorder.Code)
	}

	invalidLimitRequest := httptest.NewRequest(http.MethodGet, "/api/community/feed?limit=13", nil)
	invalidLimitRecorder := httptest.NewRecorder()
	gateway.handleCommunityFeed(invalidLimitRecorder, invalidLimitRequest)
	if invalidLimitRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid feed limit status = %d, want 400", invalidLimitRecorder.Code)
	}

	postRequest := httptest.NewRequest(http.MethodPost, "/api/community/feed", nil)
	postRecorder := httptest.NewRecorder()
	gateway.handleCommunityFeed(postRecorder, postRequest)
	if postRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST feed status = %d, want 405", postRecorder.Code)
	}
	if postRecorder.Header().Get("Allow") != http.MethodGet || postRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("POST feed safety headers = %#v", postRecorder.Header())
	}
}
