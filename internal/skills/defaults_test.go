package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func defaultSkillTestFS(body, script string) fs.FS {
	return fstest.MapFS{
		"default-skills/example/SKILL.md": {
			Data: []byte("---\nname: example\ndescription: Example default skill.\n---\n\n" + body + "\n"),
		},
		"default-skills/example/scripts/run.py": {Data: []byte(script)},
	}
}

func TestDefaultUserSkillSeedsOnceAndDeletionPersists(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".openkitty", "skills")
	first, err := seedDefaultUserSkillsFromFS(defaultSkillTestFS("# Version one", "print('v1')\n"), "default-skills", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Installed) != 1 || first.Installed[0] != "example" {
		t.Fatalf("first seed = %+v", first)
	}
	target := filepath.Join(root, "example")
	if _, err := os.Stat(filepath.Join(target, "scripts", "run.py")); err != nil {
		t.Fatalf("seeded runtime asset missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, defaultUserSkillStateFile)); err != nil {
		t.Fatalf("state file missing: %v", err)
	}

	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	second, err := seedDefaultUserSkillsFromFS(defaultSkillTestFS("# Version two", "print('v2')\n"), "default-skills", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.PreviouslyRemoved) != 1 || second.PreviouslyRemoved[0] != "example" {
		t.Fatalf("deleted skill was not remembered: %+v", second)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("deleted default skill was recreated: %v", err)
	}
}

func TestDefaultUserSkillUpdatesOnlyUntouchedManagedCopy(t *testing.T) {
	t.Run("untouched", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "skills")
		if _, err := seedDefaultUserSkillsFromFS(defaultSkillTestFS("# Version one", "print('v1')\n"), "default-skills", root); err != nil {
			t.Fatal(err)
		}
		result, err := seedDefaultUserSkillsFromFS(defaultSkillTestFS("# Version two", "print('v2')\n"), "default-skills", root)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Updated) != 1 || result.Updated[0] != "example" {
			t.Fatalf("untouched update = %+v", result)
		}
		data, err := os.ReadFile(filepath.Join(root, "example", "scripts", "run.py"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "print('v2')\n" {
			t.Fatalf("updated script = %q", data)
		}
	})

	t.Run("edited", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "skills")
		if _, err := seedDefaultUserSkillsFromFS(defaultSkillTestFS("# Version one", "print('v1')\n"), "default-skills", root); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(root, "example", "scripts", "run.py")
		if err := os.WriteFile(script, []byte("print('user edit')\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result, err := seedDefaultUserSkillsFromFS(defaultSkillTestFS("# Version two", "print('v2')\n"), "default-skills", root)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Preserved) != 1 || result.Preserved[0] != "example" {
			t.Fatalf("edited update = %+v", result)
		}
		data, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "print('user edit')\n" {
			t.Fatalf("user edit was overwritten: %q", data)
		}
	})
}

func TestDefaultUserSkillPreservesPreexistingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	target := filepath.Join(root, "example")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("---\nname: example\ndescription: User skill.\n---\n\n# User version\n")
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := seedDefaultUserSkillsFromFS(defaultSkillTestFS("# Default", "print('default')\n"), "default-skills", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Preserved) != 1 || result.Preserved[0] != "example" {
		t.Fatalf("preexisting result = %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(custom) {
		t.Fatal("preexisting user skill was overwritten")
	}
}

func TestEmbeddedDefaultUserSkillCatalogIsEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".openkitty", "skills")
	result, err := seedDefaultUserSkillsFromFS(defaultUserSkillsFS, "default-skills", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installed) != 0 || len(result.Updated) != 0 || len(result.Preserved) != 0 || len(result.PreviouslyRemoved) != 0 {
		t.Fatalf("empty embedded catalog produced changes: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, defaultUserSkillStateFile)); err != nil {
		t.Fatalf("empty catalog state file missing: %v", err)
	}
}
