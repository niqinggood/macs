package network

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"

	"mac/internal/textutil"
)

type retrievalGoalContextKey struct{}

var (
	currentNewsBriefPattern        = regexp.MustCompile(`(?i)(?:新闻|热点|要闻|快讯|资讯|news|headline|briefing)`)
	ErrPrivateNetworkAccessBlocked = errors.New("private network access blocked")
)

// WithRetrievalGoal records the user's bounded retrieval objective for network
// tools that need to tune result breadth without depending on an agent type.
func WithRetrievalGoal(ctx context.Context, goal string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ctx
	}
	return context.WithValue(ctx, retrievalGoalContextKey{}, textutil.Truncate(goal, 1200))
}

// RetrievalGoal returns the request objective attached by WithRetrievalGoal.
func RetrievalGoal(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	goal, _ := ctx.Value(retrievalGoalContextKey{}).(string)
	return strings.TrimSpace(goal)
}

// IsCurrentNewsBrief reports whether a retrieval objective requests a current
// news or headline briefing.
func IsCurrentNewsBrief(goal string) bool {
	return currentNewsBriefPattern.MatchString(goal)
}

// IsPrivateHost reports whether a URL host resolves lexically to a local or
// private IP address. DNS resolution policy remains the caller's responsibility.
func IsPrivateHost(host string) bool {
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	ip := net.ParseIP(host)
	if ip == nil {
		host = strings.ToLower(host)
		return host == "localhost" || host == "127.0.0.1" || host == "[::1]" ||
			strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal")
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
