package toolcontract

import (
	"strings"
	"testing"
)

func TestSearchToolSummariesUsesAliasesAndChineseCapabilityTerms(t *testing.T) {
	summaries := []map[string]string{
		{
			"name":    "extract",
			"desc":    "Extract documents and fill DOCX templates with placeholder values.",
			"aliases": "word_template_fill docx_template_fill Word模板填充 Word下划线填写",
			"tags":    "word docx document template placeholder underline fill 文档 模板 表格 下划线 填写",
		},
		{
			"name": "database_query",
			"desc": "Run a read-only SQL query.",
		},
		{
			"name": "generic_template",
			"desc": "Fill reusable prompt templates.",
		},
	}

	for _, query := range []string{"Word模板填写", "中文文档模板填充", "LIMS下划线填写", "docx_template_fill"} {
		result := SearchToolSummaries(summaries, query)
		if !strings.Contains(result, "**extract**") {
			t.Fatalf("query %q did not find extract via bilingual metadata:\n%s", query, result)
		}
		if query == "Word模板填写" && strings.Contains(result, "**generic_template**") {
			t.Fatalf("Word capability constraint was ignored:\n%s", result)
		}
	}
}

func TestSearchToolSummariesFindsToolDetailByHermesStyleAlias(t *testing.T) {
	summaries := []map[string]string{
		{
			"name":    "tool_detail",
			"desc":    "Get tool schemas and discover tools.",
			"aliases": "tool_search search_tools 工具搜索 工具查找",
			"tags":    "tool discovery schema 能力发现 工具参数",
		},
	}
	result := SearchToolSummaries(summaries, "tool_search")
	if !strings.Contains(result, "**tool_detail**") {
		t.Fatalf("Hermes-style alias did not resolve tool_detail:\n%s", result)
	}
}

func TestSearchToolSummariesKeepsExactNameRanking(t *testing.T) {
	summaries := []map[string]string{
		{"name": "extract_more", "desc": "Extract more data."},
		{"name": "extract", "desc": "Extract data."},
	}
	result := SearchToolSummaries(summaries, "extract")
	if strings.Index(result, "**extract**") > strings.Index(result, "**extract_more**") {
		t.Fatalf("exact name should rank first:\n%s", result)
	}
}
