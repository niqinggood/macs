package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSourceAcquireNovaReceiptPublishedDomainAndFrozenEvidence(t *testing.T) {
	const sourceURL = "https://www.sicurrent.com/nova/chatroom/posts/731"
	frozenPath := filepath.Join(".openkitty", "storage", ".system", "pipeline_sources", "source.json")
	frozenFullPath := filepath.Join(t.TempDir(), frozenPath)
	matches := func(candidate, relative, absolute string) bool {
		return candidate == relative || candidate == absolute
	}
	setSourceURLs := func(receipt, bundle map[string]interface{}, source string) {
		receipt["selected_source_url"] = source
		receipt["selected"].(map[string]interface{})["source_url"] = source
		bundle["matches"].([]map[string]interface{})[0]["source_url"] = source
	}

	tests := []struct {
		name   string
		mutate func(receipt, bundle map[string]interface{})
		want   bool
	}{
		{name: "complete_published_source", want: true},
		{
			name: "host_suffix_spoof",
			mutate: func(receipt, bundle map[string]interface{}) {
				setSourceURLs(receipt, bundle, "https://www.sicurrent.com.attacker.example/nova/chatroom/posts/731")
			},
		},
		{
			name: "host_in_userinfo_is_not_the_host",
			mutate: func(receipt, bundle map[string]interface{}) {
				setSourceURLs(receipt, bundle, "https://www.sicurrent.com@attacker.example/nova/chatroom/posts/731")
			},
		},
		{
			name: "http_is_not_an_authenticated_source",
			mutate: func(receipt, bundle map[string]interface{}) {
				setSourceURLs(receipt, bundle, "http://www.sicurrent.com/nova/chatroom/posts/731")
			},
		},
		{
			name: "retired_domain_is_not_the_published_source",
			mutate: func(receipt, bundle map[string]interface{}) {
				setSourceURLs(receipt, bundle, "https://www.novaexel.com/nova/chatroom/posts/731")
			},
		},
		{
			name: "url_post_id_does_not_match_selected_id",
			mutate: func(receipt, bundle map[string]interface{}) {
				setSourceURLs(receipt, bundle, "https://www.sicurrent.com/nova/chatroom/posts/732")
			},
		},
		{
			name: "bundle_post_does_not_match_receipt",
			mutate: func(receipt, bundle map[string]interface{}) {
				selected := bundle["matches"].([]map[string]interface{})[0]
				selected["post_id"] = int64(732)
				selected["source_url"] = "https://www.sicurrent.com/nova/chatroom/posts/732"
			},
		},
		{
			name: "bundle_url_does_not_match_its_post_id",
			mutate: func(receipt, bundle map[string]interface{}) {
				bundle["matches"].([]map[string]interface{})[0]["source_url"] = "https://www.sicurrent.com/nova/chatroom/posts/732"
			},
		},
		{
			name: "changed_content_even_with_fresh_bundle_hash",
			mutate: func(receipt, bundle map[string]interface{}) {
				bundle["matches"].([]map[string]interface{})[0]["content"] = "A replacement body that was not selected."
			},
		},
		{
			name: "receipt_points_to_another_frozen_file",
			mutate: func(receipt, bundle map[string]interface{}) {
				receipt["output_path"] = frozenFullPath + ".other"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const (
				query     = "Find the complete post about task execution."
				content   = "# Task execution\n\nThe full source body, including its evidence and conclusions."
				fetchedAt = "2026-10-19T08:00:00Z"
			)
			contentHash := sha256.Sum256([]byte(content))
			contentSHA := hex.EncodeToString(contentHash[:])
			bundle := map[string]interface{}{
				"status": "ready", "source": "nova_chatroom", "query": query,
				"fetched_at": fetchedAt, "match_count": 1,
				"matches": []map[string]interface{}{{
					"post_id": int64(731), "source_url": sourceURL,
					"content_sha256": contentSHA, "content": content,
				}},
			}
			receipt := map[string]interface{}{
				"ok": true, "source": "nova_chatroom", "query": query,
				"fetched_at": fetchedAt, "match_count": 1,
				"selected": map[string]interface{}{
					"post_id": int64(731), "source_url": sourceURL, "content_sha256": contentSHA,
				},
				"selected_post_id": int64(731), "selected_source_url": sourceURL,
				"selected_content_sha256": contentSHA, "output_path": frozenFullPath,
			}
			if test.mutate != nil {
				test.mutate(receipt, bundle)
			}

			// Rehash the final mutated bundle, as the real freezing tool does.
			// Invalid URLs and IDs must fail their own evidence checks, rather
			// than being rejected merely because an old file hash was retained.
			frozenData, err := json.MarshalIndent(bundle, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			frozenData = append(frozenData, '\n')
			bundleHash := sha256.Sum256(frozenData)
			bundleSHA := hex.EncodeToString(bundleHash[:])
			receipt["whole_file_sha256"], receipt["file_sha256"] = bundleSHA, bundleSHA
			receipt["file_bytes"] = len(frozenData)
			result, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			params, err := json.Marshal(map[string]string{"description": query, "output_path": frozenPath})
			if err != nil {
				t.Fatal(err)
			}
			call := SourceAcquireCall{ToolName: "source_acquire", Result: string(result), Params: string(params)}
			if got := ValidSourceAcquireEvidence(call, frozenPath, frozenFullPath, frozenData, matches); got != test.want {
				t.Fatalf("ValidSourceAcquireEvidence() = %v, want %v", got, test.want)
			}
		})
	}
}
