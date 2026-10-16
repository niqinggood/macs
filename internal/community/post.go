package community

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	knowledgetools "mac/internal/tools/knowledge"
)

type communityArticle struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	CreatedAt    string `json:"created_at"`
	Content      string `json:"content"`
	ContentType  string `json:"content_type"`
	CommunityURL string `json:"community_url"`
}

func communityReadingURL(id int64) string {
	return "/web/community.html?id=" + strconv.FormatInt(id, 10)
}

func fetchCommunityArticle(ctx context.Context, id int64) (communityArticle, error) {
	return fetchCommunityArticleWithTool(ctx, knowledgetools.NewNovaChatroomSearchTool("."), id)
}

func fetchCommunityArticleWithTool(ctx context.Context, tool *knowledgetools.NovaChatroomSearchTool, id int64) (communityArticle, error) {
	post, err := tool.FetchPost(ctx, id)
	if err != nil {
		return communityArticle{}, err
	}
	if len(post.Content) > 256*1024 {
		return communityArticle{}, errors.New("community article exceeds reading limit")
	}
	title, _ := communityPostPreview(post.Content)
	article := communityArticle{ID: post.ID, Title: title, Author: post.Author, CreatedAt: post.CreatedAt, Content: post.Content, ContentType: post.ContentType}
	base, err := url.Parse(tool.CanonicalBaseURL())
	if err == nil && base.Host != "" && (base.Scheme == "https" || base.Scheme == "http") {
		article.CommunityURL = base.ResolveReference(&url.URL{Path: "/chatroom/"}).String()
	}
	return article, nil
}

// PostHandler serves public community content only, without accepting an
// upstream address, forwarding credentials, or initiating model calls.
func PostHandler(w http.ResponseWriter, r *http.Request) {
	handleCommunityPost(w, r, fetchCommunityArticle)
}

func handleCommunityPost(w http.ResponseWriter, r *http.Request, fetch func(context.Context, int64) (communityArticle, error)) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "error": "method not allowed"})
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	id, parseErr := strconv.ParseInt(strings.TrimSpace(query.Get("id")), 10, 64)
	if err != nil || parseErr != nil || id <= 0 || len(query) != 1 || len(query["id"]) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "invalid post id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	article, err := fetch(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"ok": false, "error": "文章暂时无法读取，请稍后重试"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "post": article})
}
