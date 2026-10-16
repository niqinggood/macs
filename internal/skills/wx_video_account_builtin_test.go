package skills

import (
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	rootassets "mac"
)

func TestBuiltinIndexExposesWechatVideoAccountSkill(t *testing.T) {
	data, err := os.ReadFile("builtin/OPENKITTY_INDEX.json")
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]struct {
		Desc   string   `json:"desc"`
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	media, ok := index["media"]
	if !ok {
		t.Fatal("missing media category")
	}
	for _, want := range []string{"wx-video-account-notes", "video-note", "ocr-document", "funasr-speech"} {
		if !containsString(media.Skills, want) {
			t.Fatalf("media index missing %q: %v", want, media.Skills)
		}
	}
}

func TestEmbeddedWechatVideoAccountSkillIncludesRuntime(t *testing.T) {
	root := "builtin/media/wx-video-account-notes"
	for _, rel := range []string{
		"SKILL.md",
		"agents/openai.yaml",
		"scripts/bootstrap.ps1",
		"scripts/run.ps1",
		"scripts/invoke_pipeline.ps1",
		"runtime/pipeline.py",
		"runtime/resolve_link.py",
		"runtime/assets_manifest.json",
		"resources/model-note-template.md",
	} {
		if _, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/"+rel); err != nil {
			t.Fatalf("embedded skill missing %s: %v", rel, err)
		}
	}
	metadata := SkillMetadata{Name: "wx-video-account-notes"}
	if !EmbeddedSkillHasRuntimeAssets(rootassets.BuiltinSkillsFS, root, metadata) {
		t.Fatal("WeChat Video Account skill runtime assets were not detected")
	}
}

func TestEmbeddedWechatVideoAccountSkillLoadsAndRanksFirst(t *testing.T) {
	loader := NewSkillsLoader(t.TempDir(), "Researcher", "")
	if err := loader.LoadBuiltinSkillsFromFS(rootassets.BuiltinSkillsFS, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("wx-video-account-notes")
	if err != nil {
		t.Fatal(err)
	}
	if !skill.Embedded || !skill.RequiresRuntime {
		t.Fatalf("loaded skill should be an embedded runtime skill: %#v", skill)
	}
	top := loader.GetTopSkillsForQuery("https://weixin.qq.com/sph/AZhhbswU8G 提取这个的内容", 8)
	if len(top) == 0 || top[0] != "wx-video-account-notes" {
		t.Fatalf("dedicated WeChat Video Account skill should rank first, got %v", top)
	}
	if prompt := loader.BuildIndexPrompt(); !strings.Contains(prompt, "wx-video-account-notes") {
		t.Fatalf("builtin index prompt omitted dedicated skill: %s", prompt)
	}
}

func TestWechatVideoAccountSkillForbidsWebFetchOnlyCompletion(t *testing.T) {
	data, err := os.ReadFile("builtin/media/wx-video-account-notes/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"weixin.qq.com/sph/",
		"note_materials.json",
		"powershell.exe -NoProfile -ExecutionPolicy Bypass",
		"不得调用 `pwsh`",
		"仅拿到网页配文或标签",
		"不得声称已提取视频内容",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("skill contract missing %q", want)
		}
	}
}
