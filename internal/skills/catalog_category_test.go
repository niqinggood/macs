package skills

import (
	"reflect"
	"testing"

	rootassets "mac"
)

func TestSkillCatalogCategoryUsesPurposeBeforeIncidentalDescription(t *testing.T) {
	tests := []struct {
		name, display, description string
		tags, indexed              []string
		dir, want                  string
	}{
		{name: "quotation-builder", description: "软件开发报价，不用于股票行情。", tags: []string{"quotation", "AI"}, indexed: []string{"business"}, dir: "builtin/business-finance/quotation-builder", want: "business"},
		{name: "team-research", indexed: []string{"research"}, want: "research"},
		{name: "agent-browser", indexed: []string{"web-automation"}, want: "research"},
		{name: "agent-reach", indexed: []string{"web-automation"}, want: "research"},
		{name: "relationship-mentor", tags: []string{"communication"}, want: "life"},
		{name: "factor-investment", want: "finance"},
		{name: "provider-sdk", dir: `builtin\business-finance\provider-sdk`, want: "finance"},
		{name: "自定义助手", tags: []string{"办公文档"}, description: "用数据和代码处理文档", want: "office"},
		{name: "custom-helper", description: "整理每周的投资组合", want: "finance"},
		{name: "custom-helper", description: "用户定义的专有任务", want: "other"},
		{name: "display", display: "视频笔记", want: "creative"},
		{name: "multi-purpose", indexed: []string{"business", "research"}, want: "research"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SkillCatalogCategory(SkillMetadata{Name: tt.name, Description: tt.description, Tags: tt.tags}, tt.display, tt.indexed, tt.dir)
			if got != tt.want {
				t.Fatalf("category = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSkillCatalogSnapshotCategoriesAreStableAndDoNotChangeRouting(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "Assistant", "")
	if err := loader.LoadBuiltinSkillsFromFS(rootassets.BuiltinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	before := loader.GetTopSkillsForQuery("填写 Word 模板", 5)
	first := loader.SnapshotSkills()
	second := loader.SnapshotSkills()
	if len(first) == 0 || !reflect.DeepEqual(first, second) {
		t.Fatal("catalog snapshots must be populated and stable")
	}
	for _, snapshot := range first {
		if catalogCategoryHint(snapshot.Category) == "" {
			t.Fatalf("skill %s has unknown category %q", snapshot.Name, snapshot.Category)
		}
	}
	if after := loader.GetTopSkillsForQuery("填写 Word 模板", 5); !reflect.DeepEqual(before, after) {
		t.Fatal("catalog classification changed skill routing")
	}
}
