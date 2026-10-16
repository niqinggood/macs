package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeReloadSkill(t *testing.T, root, name, description, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func snapshotSkillByName(t *testing.T, loader *SkillsLoader, name string) SkillSnapshot {
	t.Helper()
	for _, skill := range loader.SnapshotSkills() {
		if skill.Name == name {
			return skill
		}
	}
	t.Fatalf("skill %q not found in snapshot: %#v", name, loader.SnapshotSkills())
	return SkillSnapshot{}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
