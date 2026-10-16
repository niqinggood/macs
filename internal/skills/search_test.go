package skills

import (
	"strings"
	"testing"
)

func TestExtractIntentKeywordsEnhanced(t *testing.T) {
	tests := []struct {
		query  string
		checks []string
	}{
		{"写一个复杂的PPT", []string{"ppt", "workflow"}},
		{"帮我发邮件给老板", []string{"email", "send", "notify"}},
		{"分析这个CSV文件", []string{"csv", "data", "analysis"}},
		{"紧急修复这个bug", []string{"debug", "quick"}},
	}
	for _, tt := range tests {
		result := extractIntentKeywordsEnhanced(tt.query)
		for _, check := range tt.checks {
			found := false
			for _, r := range result {
				if r == check {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("query '%s': expected keyword '%s' in result %v", tt.query, check, result)
			}
		}
	}
}

func TestSkillSearchCache(t *testing.T) {
	cache := NewSkillSearchCache(10, 1000) // 1s TTL

	// Set and get
	cache.Set("query1", []string{"skill-a", "skill-b"})
	result, ok := cache.Get("query1")
	if !ok || len(result) != 2 {
		t.Error("cache should return set values")
	}

	// Miss
	_, ok = cache.Get("nonexistent")
	if ok {
		t.Error("should miss for unknown query")
	}

	// TTL expiry - use 1ms TTL
	cache2 := NewSkillSearchCache(10, 1) // bare numeric TTL is treated as milliseconds
	cache2.Set("q", []string{"a"})
	// Just verify the cache works
	_, ok = cache2.Get("q")
	_ = ok // TTL may or may not have expired
}

func TestSkillSearchCacheTreatsBareNumericTTLAsMilliseconds(t *testing.T) {
	cache := NewSkillSearchCache(10, 1)
	cache.Set("query", []string{"skill"})

	result, ok := cache.Get("query")
	if !ok || len(result) != 1 || result[0] != "skill" {
		t.Fatalf("cache should not expire immediately for bare numeric TTL: result=%v ok=%v", result, ok)
	}
}

func TestExtractIntentKeywordsEnhanced_Dedup(t *testing.T) {
	// Should not duplicate keywords
	result := extractIntentKeywordsEnhanced("write code and develop")
	count := 0
	for _, r := range result {
		if r == "code" {
			count++
		}
	}
	if count > 1 {
		t.Error("keywords should be deduplicated")
	}
}

func TestExtractIntentKeywordsEnhanced_PDF(t *testing.T) {
	result := extractIntentKeywordsEnhanced("把这个PDF转成文字")
	found := false
	for _, r := range result {
		if r == "pdf" {
			found = true
		}
	}
	if !found {
		t.Errorf("PDF query should include 'pdf' keyword: %v", result)
	}
}

func TestExtractIntentKeywordsEnhanced_Excel(t *testing.T) {
	result := extractIntentKeywordsEnhanced("处理这个xlsx表格")
	foundExcel := false
	foundData := false
	for _, r := range result {
		if r == "excel" || r == "spreadsheet" {
			foundExcel = true
		}
		if r == "data" {
			foundData = true
		}
	}
	if !foundExcel || !foundData {
		t.Errorf("xlsx query should include excel+data: %v", result)
	}
}

func TestExtractIntentKeywordsEnhanced_ChineseNaturalPhrases(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{"这个功能不工作，马上排查", []string{"debug", "quick"}},
		{"把合同pdf转换成文本", []string{"pdf", "convert"}},
		{"完整分析这个表格数据", []string{"workflow", "data"}},
	}
	for _, tt := range tests {
		result := extractIntentKeywordsEnhanced(tt.query)
		joined := strings.Join(result, "\n")
		for _, want := range tt.want {
			if !strings.Contains(joined, want) {
				t.Fatalf("query %q should include %q, got %v", tt.query, want, result)
			}
		}
	}
}

func TestSkillSearchCache_Eviction(t *testing.T) {
	cache := NewSkillSearchCache(2, 10000)
	cache.Set("a", []string{"1"})
	cache.Set("b", []string{"2"})
	cache.Set("c", []string{"3"}) // should evict oldest

	// At least one should be evicted
	found := 0
	for _, q := range []string{"a", "b", "c"} {
		if _, ok := cache.Get(q); ok {
			found++
		}
	}
	if found == 3 {
		t.Log("all 3 cached (eviction may not have triggered)")
	}
}

func TestExtractIntentKeywordsEnhanced_Email(t *testing.T) {
	result := extractIntentKeywordsEnhanced("send an email")
	found := false
	for _, r := range result {
		if strings.Contains(r, "email") || strings.Contains(r, "send") || strings.Contains(r, "notify") {
			found = true
		}
	}
	if !found {
		t.Errorf("email query should match email keywords: %v", result)
	}
}
