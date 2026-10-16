package community

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	knowledgetools "mac/internal/tools/knowledge"
)

const (
	communityFeedMaximumPosts = 12
	communityFeedFreshFor     = 90 * time.Second
	communityFeedStaleFor     = 6 * time.Hour
)

type communityFeedPost struct {
	ID              int64  `json:"id"`
	TopicID         int64  `json:"topic_id,omitempty"`
	Title           string `json:"title"`
	Summary         string `json:"summary"`
	Author          string `json:"author,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	SourceURL       string `json:"source_url"`
	ReadingURL      string `json:"reading_url"`
	TechnologyScore int    `json:"technology_score"`
}

type communityFeedEntry struct {
	Posts     []communityFeedPost
	FetchedAt time.Time
	ExpiresAt time.Time
}

type communityFeedStore struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	entry     communityFeedEntry
	fetch     func(context.Context, int) ([]communityFeedPost, error)
	now       func() time.Time
}

func newCommunityFeedStore(fetch func(context.Context, int) ([]communityFeedPost, error)) *communityFeedStore {
	return &communityFeedStore{fetch: fetch, now: time.Now}
}

var defaultCommunityFeedStore = newCommunityFeedStore(fetchNovaCommunityFeed)

var (
	communityMarkdownImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	communityMarkdownLinkPattern  = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	communityBareURLPattern       = regexp.MustCompile(`https?://[^\s<>()]+`)
)

func (s *communityFeedStore) load(ctx context.Context) (communityFeedEntry, bool, error) {
	if s == nil || s.fetch == nil {
		return communityFeedEntry{}, false, errors.New("community feed is not configured")
	}
	now := s.currentTime()
	if entry, ok := s.freshEntry(now); ok {
		return entry, false, nil
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	now = s.currentTime()
	if entry, ok := s.freshEntry(now); ok {
		return entry, false, nil
	}

	posts, err := s.fetch(ctx, communityFeedMaximumPosts)
	if err != nil || len(posts) == 0 {
		if stale, ok := s.staleEntry(now); ok {
			return stale, true, nil
		}
		if err == nil {
			err = errors.New("community feed returned no technology posts")
		}
		return communityFeedEntry{}, false, err
	}
	if len(posts) > communityFeedMaximumPosts {
		posts = posts[:communityFeedMaximumPosts]
	}
	entry := communityFeedEntry{
		Posts:     cloneCommunityFeedPosts(posts),
		FetchedAt: now.UTC(),
		ExpiresAt: now.Add(communityFeedFreshFor),
	}
	s.mu.Lock()
	s.entry = entry
	s.mu.Unlock()
	return cloneCommunityFeedEntry(entry), false, nil
}

func (s *communityFeedStore) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *communityFeedStore) freshEntry(now time.Time) (communityFeedEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entry.Posts) == 0 || !now.Before(s.entry.ExpiresAt) {
		return communityFeedEntry{}, false
	}
	return cloneCommunityFeedEntry(s.entry), true
}

func (s *communityFeedStore) staleEntry(now time.Time) (communityFeedEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entry.Posts) == 0 || now.Sub(s.entry.FetchedAt) > communityFeedStaleFor {
		return communityFeedEntry{}, false
	}
	return cloneCommunityFeedEntry(s.entry), true
}

func cloneCommunityFeedEntry(entry communityFeedEntry) communityFeedEntry {
	entry.Posts = cloneCommunityFeedPosts(entry.Posts)
	return entry
}

func cloneCommunityFeedPosts(posts []communityFeedPost) []communityFeedPost {
	return append([]communityFeedPost(nil), posts...)
}

func communityFeedETag(posts []communityFeedPost, fetchedAt time.Time, stale bool) string {
	payload, _ := json.Marshal(struct {
		Posts     []communityFeedPost `json:"posts"`
		FetchedAt string              `json:"fetched_at"`
		Stale     bool                `json:"stale"`
	}{
		Posts:     posts,
		FetchedAt: fetchedAt.Format(time.RFC3339Nano),
		Stale:     stale,
	})
	hash := sha256.Sum256(payload)
	return `"` + hex.EncodeToString(hash[:12]) + `"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"ok": false, "error": "method not allowed"})
		return
	}
	limit := 8
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > communityFeedMaximumPosts {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "limit must be between 1 and 12"})
			return
		}
		limit = parsed
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	entry, stale, err := defaultCommunityFeedStore.load(ctx)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"ok":    false,
			"error": "科技社区内容暂时不可用，请稍后重试",
		})
		return
	}
	posts := entry.Posts
	if len(posts) > limit {
		posts = posts[:limit]
	}
	responseETag := communityFeedETag(posts, entry.FetchedAt, stale)
	if !stale && responseETag != "" && r.Header.Get("If-None-Match") == responseETag {
		w.Header().Set("ETag", responseETag)
		w.Header().Set("Cache-Control", "private, max-age=30, stale-if-error=21600")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", responseETag)
	w.Header().Set("Cache-Control", "private, max-age=30, stale-if-error=21600")
	if stale {
		w.Header().Set("Warning", `110 - "Response is stale"`)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"source":     "nova_chatroom",
		"fetched_at": entry.FetchedAt.Format(time.RFC3339),
		"stale":      stale,
		"posts":      posts,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func fetchNovaCommunityFeed(ctx context.Context, limit int) ([]communityFeedPost, error) {
	tool := knowledgetools.NewNovaChatroomSearchTool(".")
	tool.SetFeedOptions(16, 5*time.Second)
	return fetchNovaCommunityFeedWithTool(ctx, tool, limit)
}

func fetchNovaCommunityFeedWithTool(ctx context.Context, tool *knowledgetools.NovaChatroomSearchTool, limit int) ([]communityFeedPost, error) {
	if tool == nil {
		return nil, errors.New("Nova community client is not configured")
	}
	if limit < 1 || limit > communityFeedMaximumPosts {
		limit = communityFeedMaximumPosts
	}
	topics, err := tool.FetchTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch Nova community topics: %w", err)
	}
	if len(topics) == 0 {
		return nil, errors.New("Nova community returned no active topics")
	}
	technologyTopics := make([]knowledgetools.NovaChatroomTopic, 0, len(topics))
	for _, topic := range topics {
		if communityTopicIsTechnology(topic) {
			technologyTopics = append(technologyTopics, topic)
		}
	}
	if len(technologyTopics) > 0 {
		topics = technologyTopics
	}
	posts, pageErrors := tool.FetchTopicPageBatch(ctx, topics, 1)
	if len(posts) == 0 {
		if len(pageErrors) > 0 {
			return nil, fmt.Errorf("fetch Nova community posts: %w", errors.Join(pageErrors...))
		}
		return nil, errors.New("Nova community returned no posts")
	}

	deduplicated := make(map[int64]knowledgetools.NovaChatroomPost, len(posts))
	for _, post := range posts {
		if post.ID <= 0 || strings.TrimSpace(post.Content) == "" {
			continue
		}
		if previous, exists := deduplicated[post.ID]; !exists || communityPostNewer(post, previous) {
			deduplicated[post.ID] = post
		}
	}
	technologyPosts := make([]communityFeedPost, 0, len(deduplicated))
	canonicalBase := tool.CanonicalBaseURL()
	for _, post := range deduplicated {
		score := communityTechnologyScore(post.Content)
		if score == 0 {
			continue
		}
		title, summary := communityPostPreview(post.Content)
		technologyPosts = append(technologyPosts, communityFeedPost{
			ID:              post.ID,
			TopicID:         post.TopicID,
			Title:           title,
			Summary:         summary,
			Author:          strings.TrimSpace(post.Author),
			CreatedAt:       strings.TrimSpace(post.CreatedAt),
			SourceURL:       canonicalBase + "/posts/" + strconv.FormatInt(post.ID, 10),
			ReadingURL:      communityReadingURL(post.ID),
			TechnologyScore: score,
		})
	}
	sort.SliceStable(technologyPosts, func(i, j int) bool {
		left, right := technologyPosts[i], technologyPosts[j]
		leftTime, leftOK := parseCommunityPostTime(left.CreatedAt)
		rightTime, rightOK := parseCommunityPostTime(right.CreatedAt)
		if leftOK && rightOK && !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if leftOK != rightOK {
			return leftOK
		}
		if left.TechnologyScore != right.TechnologyScore {
			return left.TechnologyScore > right.TechnologyScore
		}
		return left.ID > right.ID
	})
	if len(technologyPosts) == 0 {
		return nil, errors.New("Nova community returned no technology posts")
	}
	if len(technologyPosts) > limit {
		technologyPosts = technologyPosts[:limit]
	}
	return technologyPosts, nil
}

func communityTopicIsTechnology(topic knowledgetools.NovaChatroomTopic) bool {
	category := strings.ToLower(strings.TrimSpace(topic.Category))
	switch category {
	case "ai", "technology", "tech", "robotics", "scifi", "science":
		return true
	}
	return communityTechnologyScore(topic.Title+" "+topic.Description) > 0
}

func communityPostNewer(left, right knowledgetools.NovaChatroomPost) bool {
	leftTime, leftOK := parseCommunityPostTime(left.CreatedAt)
	rightTime, rightOK := parseCommunityPostTime(right.CreatedAt)
	if leftOK && rightOK {
		return leftTime.After(rightTime)
	}
	return left.ID > right.ID
}

func parseCommunityPostTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func communityTechnologyScore(content string) int {
	lower := strings.ToLower(content)
	score := 0
	for _, keyword := range []string{
		"人工智能", "大模型", "机器人", "机械臂", "无人车", "无人小车", "具身智能", "自动驾驶",
		"芯片", "半导体", "算法", "开源", "软件", "硬件", "边缘计算", "云计算", "科技", "技术",
		"航天", "量子", "脑机", "新能源", "computer", "robot", "robotics", "model", "software",
		"hardware", "semiconductor", "autonomous", "technology", "engineering", "open source",
	} {
		if strings.Contains(lower, keyword) {
			score++
		}
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if token == "ai" || token == "llm" || token == "ros" {
			score += 2
		}
	}
	return score
}

func communityPostPreview(content string) (string, string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	cleanedLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = cleanCommunityText(line)
		if line != "" {
			cleanedLines = append(cleanedLines, line)
		}
	}
	if len(cleanedLines) == 0 {
		return "科技社区动态", ""
	}
	title := truncateCommunityText(cleanedLines[0], 72)
	titleComparable := communityComparableText(cleanedLines[0])
	bodyStart := 1
	for bodyStart < len(cleanedLines) && communityComparableText(cleanedLines[bodyStart]) == titleComparable {
		bodyStart++
	}
	summary := ""
	if bodyStart < len(cleanedLines) {
		summary = truncateCommunityText(strings.Join(cleanedLines[bodyStart:], " "), 180)
	}
	return title, summary
}

func cleanCommunityText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "#>*-+` ")
	value = communityMarkdownImagePattern.ReplaceAllString(value, "")
	value = communityMarkdownLinkPattern.ReplaceAllString(value, "$1")
	value = communityBareURLPattern.ReplaceAllString(value, "")
	replacer := strings.NewReplacer("**", "", "__", "", "`", "", "~~", "")
	value = replacer.Replace(value)
	fields := strings.Fields(value)
	if len(fields) > 1 && fields[0] == fields[1] && communityDecorativeToken(fields[0]) {
		fields = fields[1:]
	}
	return strings.Join(fields, " ")
}

func communityDecorativeToken(value string) bool {
	if value == "" || len([]rune(value)) > 4 {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return false
		}
	}
	return true
}

func communityComparableText(value string) string {
	value = strings.ToLower(value)
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), "")
}

func truncateCommunityText(value string, maximum int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maximum {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:maximum])) + "…"
}
