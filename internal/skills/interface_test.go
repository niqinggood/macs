package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"mac/internal/skillpath"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestEmbeddedSkillLoadsOptionalOpenAIInterfaceMetadata(t *testing.T) {
	files := fstest.MapFS{
		"builtin/demo/SKILL.md": {
			Data: []byte("---\nname: demo\ndescription: canonical description\n---\n# Demo\nSummarize the input.\n"),
		},
		"builtin/demo/agents/openai.yaml": {
			Data: []byte("interface:\n  display_name: 'Demo Friendly'\n  short_description: 'Short UI copy'\n  default_prompt: 'Use $demo to summarize this.'\n  icon_small: './assets/icon.svg'\n"),
		},
	}

	loader := NewSkillsLoader(t.TempDir(), "test-agent", "")
	if err := loader.LoadBuiltinSkillsFromFS(files, "builtin"); err != nil {
		t.Fatal(err)
	}
	skill, err := loader.GetSkill("demo")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Interface.DisplayName != "Demo Friendly" ||
		skill.Interface.ShortDescription != "Short UI copy" ||
		skill.Interface.DefaultPrompt != "Use $demo to summarize this." {
		t.Fatalf("embedded interface metadata was not loaded: %#v", skill.Interface)
	}
	if got := snapshotSkillByName(t, loader, "demo").Interface; got != skill.Interface {
		t.Fatalf("snapshot lost interface metadata: got=%#v want=%#v", got, skill.Interface)
	}
}

func TestSkillInterfaceMetadataHotReloadIsFaultTolerant(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	dir := writeReloadSkill(t, root, "live-ui", "version one", "# Live UI\nUse the current instructions.")
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(agentsDir, "openai.yaml")
	if err := os.WriteFile(configPath, []byte("interface:\n  display_name: Version One\n  default_prompt: First prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loader := NewSkillsLoader(workspace, "", "")
	if _, err := loader.ReloadUserSkills([]string{root}); err != nil {
		t.Fatal(err)
	}
	if got := snapshotSkillByName(t, loader, "live-ui").Interface.DisplayName; got != "Version One" {
		t.Fatalf("initial interface display name = %q", got)
	}

	writeReloadSkill(t, root, "live-ui", "version two", "# Live UI\nUse the updated instructions.")
	if err := os.WriteFile(configPath, []byte("interface:\n  display_name: [broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ReloadUserSkills(nil); err != nil {
		t.Fatalf("invalid optional UI metadata must not disable the Skill: %v", err)
	}
	broken := snapshotSkillByName(t, loader, "live-ui")
	if broken.Interface != (skillpath.SkillInterfaceMetadata{}) {
		t.Fatalf("invalid optional metadata should be ignored: %#v", broken.Interface)
	}
	if !strings.Contains(broken.Raw, "version two") {
		t.Fatalf("Skill instructions did not hot reload when UI metadata was invalid: %s", broken.Raw)
	}

	if err := os.WriteFile(configPath, []byte("interface:\n  display_name: Version Three\n  short_description: Reloaded UI\n  default_prompt: Third prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ReloadUserSkills(nil); err != nil {
		t.Fatal(err)
	}
	reloaded := snapshotSkillByName(t, loader, "live-ui").Interface
	if reloaded.DisplayName != "Version Three" || reloaded.ShortDescription != "Reloaded UI" || reloaded.DefaultPrompt != "Third prompt" {
		t.Fatalf("interface metadata did not hot reload: %#v", reloaded)
	}
}

func TestSkillInterfaceMetadataAcceptsLegacyGB18030(t *testing.T) {
	source := "interface:\n  display_name: 纸片人视觉素材\n  short_description: 中文简介\n  default_prompt: 生成一组插图。\n"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := skillpath.ParseInterfaceMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.DisplayName != "纸片人视觉素材" || metadata.ShortDescription != "中文简介" || metadata.DefaultPrompt != "生成一组插图。" {
		t.Fatalf("GB18030 metadata was not decoded: %#v", metadata)
	}
}
