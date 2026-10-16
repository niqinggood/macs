package network

import (
	"net/url"
	"strings"
	"unicode"

	securitycore "mac/internal/security"

	"golang.org/x/net/publicsuffix"
)

var externalURLPattern = securitycore.ExternalURLPattern()

func containsChinese(value string) bool {
	for _, char := range value {
		if unicode.Is(unicode.Han, char) {
			return true
		}
	}
	return false
}

func normalizeCitationURL(rawURL string) string {
	return strings.TrimRight(strings.TrimSpace(rawURL), ".,，。；;`")
}

func canonicalCitationURLKey(rawURL string) string {
	parsed, err := url.Parse(normalizeCitationURL(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if parsed.Path != "/" {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		parsed.RawPath = ""
	}
	query := parsed.Query()
	for key, values := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "spm" || lower == "gclid" || lower == "fbclid" || lower == "mc_cid" || lower == "mc_eid" {
			query.Del(key)
			continue
		}
		if lower == "f" && len(values) > 0 && strings.EqualFold(strings.TrimSpace(values[0]), "rss") {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func safePublicCitationURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	for key := range parsed.Query() {
		lower := strings.ToLower(key)
		switch lower {
		case "key", "api_key", "apikey", "token", "access_token", "refresh_token", "secret", "client_secret", "password", "passwd", "authorization", "auth":
			return false
		}
		if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.HasSuffix(lower, "_token") || strings.HasSuffix(lower, "_key") {
			return false
		}
	}
	return true
}

func sourceHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
}

func newsPublisherKey(rawURL string) string {
	host := sourceHost(rawURL)
	if host == "" {
		return ""
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil && domain != "" {
		return strings.ToLower(domain)
	}
	return host
}
