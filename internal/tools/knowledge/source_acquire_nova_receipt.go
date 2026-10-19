package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// validSourceAcquireEvidence dispatches validation for the trusted provider
// selected by source_acquire. Provider-specific receipt knowledge belongs next
// to the router adapter, not in a business pipeline or its generic contract.
type SourceAcquireCall struct {
	ToolName string
	Result   string
	Params   string
}

type FrozenPathMatcher func(candidate, frozenPath, frozenFullPath string) bool

func ValidSourceAcquireEvidence(call SourceAcquireCall, frozenPath, frozenFullPath string, frozenData []byte, matches FrozenPathMatcher) bool {
	if call.ToolName != "source_acquire" || len(frozenData) == 0 {
		return false
	}
	var envelope struct {
		Source string `json:"source"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(call.Result)), &envelope) != nil {
		return false
	}
	switch envelope.Source {
	case "nova_chatroom":
		return validNovaSourceAcquireEvidence(call, frozenPath, frozenFullPath, frozenData, matches)
	default:
		return false
	}
}

func validNovaSourceAcquireEvidence(call SourceAcquireCall, frozenPath, frozenFullPath string, frozenData []byte, matches FrozenPathMatcher) bool {
	var params struct {
		Description string `json:"description"`
		OutputPath  string `json:"output_path"`
	}
	if json.Unmarshal([]byte(call.Params), &params) != nil || strings.TrimSpace(params.Description) == "" {
		return false
	}

	var receipt struct {
		OK         bool   `json:"ok"`
		Source     string `json:"source"`
		Query      string `json:"query"`
		MatchCount int    `json:"match_count"`
		Selected   struct {
			PostID        int64  `json:"post_id"`
			SourceURL     string `json:"source_url"`
			ContentSHA256 string `json:"content_sha256"`
		} `json:"selected"`
		SelectedPostID        int64  `json:"selected_post_id"`
		SelectedSourceURL     string `json:"selected_source_url"`
		SelectedContentSHA256 string `json:"selected_content_sha256"`
		OutputPath            string `json:"output_path"`
		WholeFileSHA256       string `json:"whole_file_sha256"`
		FileSHA256            string `json:"file_sha256"`
		FileBytes             int    `json:"file_bytes"`
		FetchedAt             string `json:"fetched_at"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(call.Result)), &receipt) != nil || !receipt.OK || receipt.Source != "nova_chatroom" {
		return false
	}
	if receipt.SelectedPostID <= 0 || receipt.Selected.PostID != receipt.SelectedPostID || receipt.MatchCount <= 0 || strings.TrimSpace(receipt.FetchedAt) == "" {
		return false
	}
	if strings.TrimSpace(receipt.Query) != strings.TrimSpace(params.Description) || receipt.Selected.SourceURL != receipt.SelectedSourceURL || !strings.EqualFold(receipt.Selected.ContentSHA256, receipt.SelectedContentSHA256) {
		return false
	}
	urlMatch := regexp.MustCompile(`(?i)^https://www\.sicurrent\.com/nova/chatroom/posts/(\d+)$`).FindStringSubmatch(receipt.SelectedSourceURL)
	if len(urlMatch) != 2 {
		return false
	}
	urlPostID, err := strconv.ParseInt(urlMatch[1], 10, 64)
	if err != nil || urlPostID != receipt.SelectedPostID {
		return false
	}
	sha256Re := regexp.MustCompile(`(?i)^[a-f0-9]{64}$`)
	if !sha256Re.MatchString(receipt.SelectedContentSHA256) || !sha256Re.MatchString(receipt.WholeFileSHA256) || !sha256Re.MatchString(receipt.FileSHA256) {
		return false
	}
	bundleHash := sha256.Sum256(frozenData)
	actualFileSHA := hex.EncodeToString(bundleHash[:])
	if !strings.EqualFold(actualFileSHA, receipt.WholeFileSHA256) || !strings.EqualFold(actualFileSHA, receipt.FileSHA256) || receipt.FileBytes != len(frozenData) {
		return false
	}
	if matches == nil || !matches(params.OutputPath, frozenPath, frozenFullPath) || !matches(receipt.OutputPath, frozenPath, frozenFullPath) {
		return false
	}

	var bundle struct {
		Status     string `json:"status"`
		Source     string `json:"source"`
		Query      string `json:"query"`
		FetchedAt  string `json:"fetched_at"`
		MatchCount int    `json:"match_count"`
		Matches    []struct {
			PostID        int64  `json:"post_id"`
			SourceURL     string `json:"source_url"`
			ContentSHA256 string `json:"content_sha256"`
			Content       string `json:"content"`
		} `json:"matches"`
	}
	if json.Unmarshal(frozenData, &bundle) != nil || bundle.Status != "ready" || bundle.Source != "nova_chatroom" || len(bundle.Matches) == 0 || bundle.MatchCount != len(bundle.Matches) || receipt.MatchCount != bundle.MatchCount {
		return false
	}
	if strings.TrimSpace(bundle.Query) != strings.TrimSpace(receipt.Query) || strings.TrimSpace(bundle.FetchedAt) != strings.TrimSpace(receipt.FetchedAt) {
		return false
	}
	match := bundle.Matches[0]
	contentHash := sha256.Sum256([]byte(match.Content))
	matchURL := regexp.MustCompile(`(?i)^https://www\.sicurrent\.com/nova/chatroom/posts/(\d+)$`).FindStringSubmatch(match.SourceURL)
	if match.PostID <= 0 || len(matchURL) != 2 || matchURL[1] != strconv.FormatInt(match.PostID, 10) {
		return false
	}
	return match.PostID == receipt.SelectedPostID &&
		match.SourceURL == receipt.SelectedSourceURL &&
		strings.EqualFold(match.ContentSHA256, receipt.SelectedContentSHA256) &&
		strings.EqualFold(hex.EncodeToString(contentHash[:]), receipt.SelectedContentSHA256)
}
