package memory

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDetectArtifactFormatPreferencesRequiresDurableIntent(t *testing.T) {
	if got := DetectArtifactFormatPreferences("把这个 Word 文档设为 A4，表格行高不要变"); len(got) != 0 {
		t.Fatalf("one-off request must not become a durable preference: %#v", got)
	}

	got := DetectArtifactFormatPreferences("以后所有 Word 报告默认用微软雅黑小四号、A4 横向，沿用原模板，填充下划线时保留下划线且不改变行高列宽")
	if len(got) != 1 {
		t.Fatalf("expected one document preference, got %#v", got)
	}
	pref := got[0]
	if pref.Scope != "document" || pref.FontFamily != "微软雅黑" || pref.FontSize != "小四" || pref.PageSize != "A4" || pref.Orientation != "landscape" {
		t.Fatalf("unexpected core preference fields: %#v", pref)
	}
	if pref.TemplateRule == "" || !strings.Contains(pref.TableLayoutRule, "行高") || !strings.Contains(pref.TableLayoutRule, "列宽") {
		t.Fatalf("template/table rules were not extracted: %#v", pref)
	}
	if !strings.Contains(pref.PlaceholderRule, "保留下划线") {
		t.Fatalf("underline rule was not extracted: %#v", pref)
	}
}

func TestArtifactFormatPreferencesPersistMergeAndInject(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := InitMemoryDB(database); err != nil {
		t.Fatalf("init memory db: %v", err)
	}
	store := NewMemoryStore(t.TempDir(), database, "test-agent", 10)

	first := DetectArtifactFormatPreferences("以后 Word 文档默认用宋体小四号、A4，沿用原模板并保持原表格布局")
	if err := store.ApplyArtifactFormatPreferencesForTenant("tenant-a", "alice", first); err != nil {
		t.Fatalf("save first preference: %v", err)
	}
	second := DetectArtifactFormatPreferences("以后 Word 文档字体统一改成微软雅黑")
	if err := store.ApplyArtifactFormatPreferencesForTenant("tenant-a", "alice", second); err != nil {
		t.Fatalf("override preference: %v", err)
	}

	preferences, err := store.GetArtifactFormatPreferencesForTenant("tenant-a", "alice")
	if err != nil {
		t.Fatalf("load preferences: %v", err)
	}
	document := preferences["document"]
	if document.FontFamily != "微软雅黑" {
		t.Fatalf("latest font did not override old value: %#v", document)
	}
	if document.FontSize != "小四" || document.PageSize != "A4" || document.TemplateRule == "" || document.TableLayoutRule == "" {
		t.Fatalf("unrelated stored fields were erased: %#v", document)
	}

	context := store.GetMemoryContextForTenant("tenant-a", "alice", "", "生成报告")
	for _, want := range []string{"复杂产物格式偏好", "Word/文档", "微软雅黑", "A4", "当前请求的明确格式要求优先"} {
		if !strings.Contains(context, want) {
			t.Fatalf("memory context missing %q:\n%s", want, context)
		}
	}
	if _, err := store.GetArtifactFormatPreferencesForTenant("tenant-b", "alice"); err == nil {
		t.Fatal("preference leaked into a different tenant")
	}
}
