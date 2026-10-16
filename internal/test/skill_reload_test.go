package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	skilldomain "mac/internal/skills"
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

func TestReloadUserSkillsUpdatesDeletesAndPreservesEmbeddedBuiltin(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "external-skills")
	loader := NewSkillsLoader(workspace, "agent-a", "")
	loader.RegisterSkill("builtin-demo", &skilldomain.Skill{
		Metadata: skilldomain.SkillMetadata{Name: "builtin-demo", Description: "memory builtin"},
		Source:   "builtin", Path: "builtin://builtin-demo", Raw: "# builtin",
	})
	userDir := writeReloadSkill(t, root, "user-demo", "version one", "# User demo\nFollow version one.")

	result, err := loader.ReloadUserSkills([]string{root})
	if err != nil {
		t.Fatalf("initial transactional reload failed: %v", err)
	}
	if result.BuiltinCount != 1 || result.UserCount != 1 {
		t.Fatalf("unexpected reload counts: %#v", result)
	}
	if got := snapshotSkillByName(t, loader, "user-demo"); !strings.Contains(got.Raw, "version one") || got.Source != "user" {
		t.Fatalf("initial user skill missing: %#v", got)
	}

	writeReloadSkill(t, root, "user-demo", "version two", "# User demo\nFollow version two.")
	if _, err := loader.ReloadUserSkills(nil); err != nil {
		t.Fatalf("reload using remembered directories failed: %v", err)
	}
	if got := snapshotSkillByName(t, loader, "user-demo"); !strings.Contains(got.Raw, "version two") {
		t.Fatalf("existing user skill was not replaced: %#v", got)
	}

	if err := os.RemoveAll(userDir); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ReloadUserSkills(nil); err != nil {
		t.Fatalf("reload after delete failed: %v", err)
	}
	if _, err := loader.GetSkill("user-demo"); err == nil {
		t.Fatal("deleted user skill remained in memory")
	}
	if got := snapshotSkillByName(t, loader, "builtin-demo"); got.Source != "builtin" || got.Path != "builtin://builtin-demo" {
		t.Fatalf("embedded builtin was not preserved: %#v", got)
	}
}

func TestReloadKeepsOldTaskInstructionsWhileNewTasksSeeNewSkill(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	loader := NewSkillsLoader(workspace, "", "")
	writeReloadSkill(t, root, "live-skill", "version one", "# Live\nFollow version one.")
	if _, err := loader.ReloadUserSkills([]string{root}); err != nil {
		t.Fatal(err)
	}

	oldTask := invokedSkillTaskContext("old-task")
	oldSkill, err := loader.GetSkill("live-skill")
	if err != nil {
		t.Fatal(err)
	}
	loader.RecordInvokedSkillForContext(oldTask, "live-skill", oldSkill.Code)

	writeReloadSkill(t, root, "live-skill", "version two", "# Live\nFollow version two.")
	if _, err := loader.ReloadUserSkills(nil); err != nil {
		t.Fatal(err)
	}

	oldPrompt := loader.BuildInvokedSkillsPromptForContext(oldTask, 5, 4000)
	if !strings.Contains(oldPrompt, "version one") || strings.Contains(oldPrompt, "version two") {
		t.Fatalf("in-flight task instructions changed across reload:\n%s", oldPrompt)
	}
	newSkill, err := loader.GetSkill("live-skill")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(newSkill.Code, "version two") || strings.Contains(newSkill.Code, "version one") {
		t.Fatalf("new task did not see the reloaded skill: %s", newSkill.Code)
	}
	if freshPrompt := loader.BuildInvokedSkillsPromptForContext(invokedSkillTaskContext("new-task"), 5, 4000); freshPrompt != "" {
		t.Fatalf("new task inherited old invocation state:\n%s", freshPrompt)
	}
}

func TestSkillCatalogReadsStayConsistentDuringReload(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	dir := writeReloadSkill(t, root, "live-skill", "live capability version zero", "# Live\nCurrent instructions.")
	loader := NewSkillsLoader(workspace, "", "")
	if _, err := loader.ReloadUserSkills([]string{root}); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	readerErr := make(chan error, 1)
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snapshots := loader.GetTopSkillPromptMatchesForQuery("live-skill", 8)
				if len(snapshots) != 1 || snapshots[0].Name != "live-skill" || snapshots[0].Description == "" {
					select {
					case readerErr <- fmt.Errorf("inconsistent skill snapshot: %#v", snapshots):
					default:
					}
					return
				}
				if count := loader.SkillCount(); count != 1 {
					select {
					case readerErr <- fmt.Errorf("inconsistent skill count: %d", count):
					default:
					}
					return
				}
			}
		}()
	}

	for version := 1; version <= 30; version++ {
		content := fmt.Sprintf("---\nname: live-skill\ndescription: live capability version %d\n---\n\n# Live\nCurrent instructions %d.\n", version, version)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
		if _, err := loader.ReloadUserSkills(nil); err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	select {
	case err := <-readerErr:
		t.Fatal(err)
	default:
	}
}

func TestReloadUserSkillsRejectsBrokenSnapshotWithoutChangingLiveCatalog(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	loader := NewSkillsLoader(workspace, "agent-a", "")
	dir := writeReloadSkill(t, root, "safe-skill", "stable", "# Safe\nKeep the stable instructions.")
	if _, err := loader.ReloadUserSkills([]string{root}); err != nil {
		t.Fatal(err)
	}
	before := snapshotSkillByName(t, loader, "safe-skill").Raw
	broken := "---\nname: [broken\n---\n# Unsafe update\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ReloadUserSkills(nil); err == nil || !strings.Contains(err.Error(), "invalid YAML frontmatter") {
		t.Fatalf("broken user skill should reject the complete snapshot: %v", err)
	}
	if after := snapshotSkillByName(t, loader, "safe-skill").Raw; after != before {
		t.Fatalf("failed reload changed live catalog\nbefore=%q\nafter=%q", before, after)
	}
}

func TestReloadUserSkillsRejectsFrontmatterNameMismatchWithoutEchoingIt(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	dir := filepath.Join(root, "expected-name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	declaredName := "secret-value-that-must-not-be-echoed"
	document := "---\nname: " + declaredName + "\ndescription: mismatch\n---\n# Body\nSafe instructions.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewSkillsLoader(workspace, "", "").ReloadUserSkills([]string{root})
	if err == nil {
		t.Fatal("mismatched frontmatter name should reject the user skill")
	}
	if !strings.Contains(err.Error(), "frontmatter name must match its directory name") {
		t.Fatalf("mismatch error was not actionable: %v", err)
	}
	if strings.Contains(err.Error(), declaredName) {
		t.Fatalf("mismatch error leaked the declared frontmatter name: %v", err)
	}
}

func TestLoadAllSkillsRejectsUserFrontmatterNameMismatch(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "skills")
	dir := filepath.Join(root, "expected-name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	document := "---\nname: different-name\ndescription: mismatch\n---\n# Body\nSafe instructions.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}

	loader := NewSkillsLoader(workspace, "", "")
	if err := loader.LoadAllSkills(root, "user"); err == nil || !strings.Contains(err.Error(), "frontmatter name must match its directory name") {
		t.Fatalf("disk user skill mismatch should return a clear error: %v", err)
	}
	if _, err := loader.GetSkill("expected-name"); err == nil {
		t.Fatal("mismatched user skill was loaded")
	}
}

func TestReloadUserSkillsHonorsConfiguredDirectoryPriority(t *testing.T) {
	workspace := t.TempDir()
	first := filepath.Join(workspace, "first")
	second := filepath.Join(workspace, "second")
	writeReloadSkill(t, first, "shared", "first wins", "# Shared\nUse the first directory.")
	writeReloadSkill(t, second, "shared", "second loses", "# Shared\nUse the second directory.")
	loader := NewSkillsLoader(workspace, "", "")
	if _, err := loader.ReloadUserSkills([]string{first, second}); err != nil {
		t.Fatal(err)
	}
	got := snapshotSkillByName(t, loader, "shared")
	if !strings.Contains(got.Raw, "first wins") || strings.Contains(got.Raw, "second loses") {
		t.Fatalf("configured directory priority was not stable: %s", got.Raw)
	}
}

func TestSkillSaveUpdateAndDeleteActivateWithoutRestart(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "agent-a", "")
	if _, err := loader.ReloadUserSkills([]string{filepath.Join(workspace, "skills")}); err != nil {
		t.Fatal(err)
	}
	agent := &LLMAgent{name: "agent-a", workspace: workspace, skillsLoader: loader}
	factory := &AgentFactory{agents: map[string]Agent{"agent-a": agent}}
	gateway := &GatewayService{
		config: &Config{Workspace: workspace},
		engine: &MultiAgentEngine{agentFactory: factory},
	}

	save := func(description string) map[string]interface{} {
		body, _ := json.Marshal(map[string]string{
			"name":    "live-skill",
			"content": "---\nname: live-skill\ndescription: " + description + "\n---\n# Live skill\nUse current instructions.\n",
		})
		recorder := httptest.NewRecorder()
		gateway.handleSkillSave(recorder, httptest.NewRequest(http.MethodPost, "/api/skill/save", bytes.NewReader(body)))
		if recorder.Code != http.StatusOK {
			t.Fatalf("save status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var response map[string]interface{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	if response := save("version one"); response["reloaded"].(float64) != 1 {
		t.Fatalf("save did not reload the active loader: %#v", response)
	}
	if got := snapshotSkillByName(t, loader, "live-skill"); !strings.Contains(got.Raw, "version one") {
		t.Fatalf("new skill was not activated: %#v", got)
	}
	save("version two")
	if got := snapshotSkillByName(t, loader, "live-skill"); !strings.Contains(got.Raw, "version two") {
		t.Fatalf("updated skill remained stale: %#v", got)
	}

	deleteRecorder := httptest.NewRecorder()
	gateway.handleSkillDelete(deleteRecorder, httptest.NewRequest(http.MethodPost, "/api/skill/delete?name=live-skill", nil))
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if _, err := loader.GetSkill("live-skill"); err == nil {
		t.Fatal("deleted skill remained active after automatic reload")
	}
}
