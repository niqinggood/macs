package skills

import (
	"strings"
	"testing"

	rootassets "mac"
)

func TestTokenizeKeywordsDropsInternalSupplementPunctuation(t *testing.T) {
	tokens := tokenizeKeywords("请列出项目中的部署环境\n\n[用户补充]: 账号")

	if !containsString(tokens, "账号") {
		t.Fatalf("expected supplement value to remain routable, got %v", tokens)
	}
	for _, token := range tokens {
		switch token {
		case "]:", "用户补充":
			t.Fatalf("internal control punctuation must not become a routing keyword: %v", tokens)
		}
	}
}

func TestWechatVideoAccountURLGetsDedicatedSkillKeyword(t *testing.T) {
	keywords := extractIntentKeywords("https://weixin.qq.com/sph/AZhhbswU8G 提取这个的内容")
	for _, want := range []string{"wx-video-account-notes", "视频号", "asr", "ocr"} {
		if !containsString(keywords, want) {
			t.Fatalf("intent keywords missing %q: %v", want, keywords)
		}
	}
}

func TestGetTopSkillsForQueryRanksChineseMetadataAheadOfBodyNoise(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	loader.skills["deployment-inventory"] = &Skill{
		Metadata: SkillMetadata{
			Name:        "deployment-inventory",
			Description: "列出项目部署环境并汇总各环境的运行状态。",
			Tags:        []string{"部署环境", "运行状态"},
			ReadWhen:    []string{"用户要求查看项目中的部署环境"},
		},
		Content: "# Deployment inventory\n\nFollow the documented inventory workflow.",
	}
	for _, name := range []string{"long-markdown-one", "long-markdown-two"} {
		loader.skills[name] = &Skill{
			Metadata: SkillMetadata{
				Name:        name,
				Description: "处理与当前请求无关的通用格式化工作。",
			},
			Content: strings.Repeat("[用户补充]: 账号\n", 200),
		}
	}

	top := loader.GetTopSkillsForQuery("请列出项目中的部署环境\n\n[用户补充]: 账号", 3)
	if len(top) == 0 || top[0] != "deployment-inventory" {
		t.Fatalf("Chinese metadata match should rank first, got %v", top)
	}
	for _, name := range top {
		if strings.HasPrefix(name, "long-markdown-") {
			t.Fatalf("body-only punctuation noise must not auto-inject a skill, got %v", top)
		}
	}
}

func TestGetTopSkillsForQueryPreservesEnglishMetadataRouting(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	loader.skills["pdf-table-extractor"] = &Skill{
		Metadata: SkillMetadata{
			Name:        "pdf-table-extractor",
			Description: "Extract text and tables from PDF documents.",
			Tags:        []string{"pdf", "table extraction"},
		},
	}
	loader.skills["body-only-decoy"] = &Skill{
		Metadata: SkillMetadata{
			Name:        "body-only-decoy",
			Description: "Unrelated workflow.",
		},
		Content: strings.Repeat("extract tables from PDF ", 100),
	}

	top := loader.GetTopSkillsForQuery("Please extract the tables from this PDF", 2)
	if len(top) == 0 || top[0] != "pdf-table-extractor" {
		t.Fatalf("English metadata match should remain routable, got %v", top)
	}
	if containsString(top, "body-only-decoy") {
		t.Fatalf("body-only decoy must not be auto-injected, got %v", top)
	}
}

func TestSkillRoutingTermsStayBoundedForLargePrompts(t *testing.T) {
	terms := skillRoutingTerms(strings.Repeat("甲乙丙丁戊己庚辛壬癸", 1000))
	if len(terms) > maxSkillRoutingTerms {
		t.Fatalf("routing term count = %d, want <= %d", len(terms), maxSkillRoutingTerms)
	}
}

func TestGetTopSkillsForQueryRoutesEmbeddedCrossDomainRequests(t *testing.T) {
	loader := NewSkillsLoader(".", "", "")
	if err := loader.LoadBuiltinSkillsFromFS(rootassets.BuiltinSkillsFS, "builtin"); err != nil {
		t.Fatalf("load embedded skills: %v", err)
	}

	tests := []struct {
		query       string
		expectedAny []string
	}{
		{
			query:       "控制机器人小车转向并检查当前状态",
			expectedAny: []string{"robot-control"},
		},
		{
			query:       "检查远程 Linux 服务器的 CPU、内存和服务健康状态",
			expectedAny: []string{"health-check", "machine-monitor", "server-patrol"},
		},
		{
			query:       "读取这个 PDF 文件并提取里面的表格",
			expectedAny: []string{"pdf-tools", "document-extraction"},
		},
		{
			query:       "排查 Go 单元测试失败的根本原因",
			expectedAny: []string{"systematic-debugging", "code-developer"},
		},
		{
			query:       "重新设计并美化这个网页界面",
			expectedAny: []string{"web-design", "taste-design"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			top := loader.GetTopSkillsForQuery(tt.query, 8)
			for _, expected := range tt.expectedAny {
				if containsString(top, expected) {
					return
				}
			}
			t.Fatalf("query %q did not route to any of %v; got %v", tt.query, tt.expectedAny, top)
		})
	}
}
