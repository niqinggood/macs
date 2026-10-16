package community

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	knowledgetools "mac/internal/tools/knowledge"
)

func TestCommunityArticleFetchesFullPostWithoutCredentials(t *testing.T) {
	for _, wrongID := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongID), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/nova/chatroom/posts/71" || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
				}
				id := 71
				if wrongID {
					id = 72
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"post": map[string]interface{}{"id": id, "topicId": 3, "content": "# Robot release\n\n## Vision\nA complete article.", "author": "Lab", "ipAddress": "private", "apiKey": "secret"}})
			}))
			defer server.Close()
			tool := knowledgetools.NewNovaChatroomSearchTool(t.TempDir())
			tool.ConfigureEndpoint(server.URL+"/nova/chatroom", "https://community.example/nova/chatroom", server.Client(), time.Second, time.Second)
			article, err := fetchCommunityArticleWithTool(context.Background(), tool, 71)
			if wrongID {
				if err == nil {
					t.Fatal("mismatched post identity accepted")
				}
				return
			}
			if err != nil || !strings.Contains(article.Content, "A complete article") || article.CommunityURL != "https://community.example/chatroom/" {
				t.Fatalf("article=%+v err=%v", article, err)
			}
			body, _ := json.Marshal(article)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), "secret") {
				t.Fatalf("private upstream fields leaked: %s", body)
			}
		})
	}
}

func TestCommunityPostHandlerRejectsInvalidAndArbitraryURLs(t *testing.T) {
	for _, query := range []string{"", "id=-1", "id=0", "id=abc", "id=1&id=2", "id=71&url=http://localhost/private", "id=999999999999999999999999"} {
		request := httptest.NewRequest(http.MethodGet, "/api/community/post?"+query, nil)
		recorder := httptest.NewRecorder()
		handleCommunityPost(recorder, request, func(context.Context, int64) (communityArticle, error) {
			t.Error("invalid request fetched upstream")
			return communityArticle{}, nil
		})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query=%q status=%d", query, recorder.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/community/post?id=71", nil)
	recorder := httptest.NewRecorder()
	handleCommunityPost(recorder, request, nil)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method boundary: %d", recorder.Code)
	}
}

func TestCommunityPostHandlerPreservesCancellationAndSafeErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/community/post?id=71", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	handleCommunityPost(recorder, request, func(ctx context.Context, id int64) (communityArticle, error) {
		if ctx.Err() != context.Canceled || id != 71 {
			t.Errorf("lost cancellation or identity")
		}
		return communityArticle{}, errors.New("internal endpoint and credential")
	})
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "credential") {
		t.Fatalf("unsafe error: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestCommunityFeedReadingLinkIsAWebPage(t *testing.T) {
	if got := communityReadingURL(71); got != "/web/community.html?id=71" {
		t.Fatalf("reading link = %q", got)
	}
}
