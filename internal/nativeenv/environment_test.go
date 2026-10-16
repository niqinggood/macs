package nativeenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	developmenttools "mac/internal/tools/development"
)

func TestNativeEnvironmentFingerprintReusesPortableDependencies(t *testing.T) {
	cacheDir := t.TempDir()
	projectOne := createNativeTestProject(t, "one", "module example.com/shared\n\ngo 1.22\n")
	projectTwo := createNativeTestProject(t, "two", "module example.com/shared\n\ngo 1.22\n")
	manager := newNativeTestManager(projectOne, cacheDir)
	setup := nativeTestSetupCommand("portable")

	first, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   projectOne,
		Runtime:      "go",
		SetupCommand: setup,
		Scope:        "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   projectTwo,
		Runtime:      "go",
		SetupCommand: setup,
		Scope:        "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("identical portable environments should share an id: %q != %q", first.ID, second.ID)
	}

	projectScopedOne, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   projectOne,
		Runtime:      "go",
		SetupCommand: setup,
		Scope:        "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectScopedTwo, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   projectTwo,
		Runtime:      "go",
		SetupCommand: setup,
		Scope:        "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if projectScopedOne.ID == projectScopedTwo.ID {
		t.Fatal("project-scoped environments must include the project identity")
	}

	if err := os.WriteFile(filepath.Join(projectTwo, "go.mod"), []byte("module example.com/changed\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   projectTwo,
		Runtime:      "go",
		SetupCommand: setup,
		Scope:        "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == first.ID {
		t.Fatal("dependency content changes must invalidate the environment id")
	}

	changedSetup, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   projectOne,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("different-recipe"),
		Scope:        "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changedSetup.ID == first.ID {
		t.Fatal("setup recipe changes must invalidate the environment id")
	}
}

func TestNativeEnvironmentPrepareReusesCompletedEntry(t *testing.T) {
	project := createNativeTestProject(t, "project", "module example.com/reuse\n\ngo 1.22\n")
	manager := newNativeTestManager(project, t.TempDir())
	spec := NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   project,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("first-build"),
		Scope:        "shared",
		PathEntries:  []string{"bin"},
		Environment:  map[string]string{"OPENKITTY_TEST_VALUE": "{env_dir}/value"},
	}

	metadata, reused, output, err := manager.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("first prepare: %v\n%s", err, output)
	}
	if reused {
		t.Fatal("first prepare unexpectedly reported a cache hit")
	}
	marker := filepath.Join(manager.entryDir(metadata.ID), "payload", "marker.txt")
	if data, readErr := os.ReadFile(marker); readErr != nil || string(data) != "first-build" {
		t.Fatalf("unexpected prepared marker: data=%q err=%v", data, readErr)
	}
	if err := os.WriteFile(marker, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}

	reusedMetadata, reused, output, err := manager.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("reuse prepare: %v\n%s", err, output)
	}
	if !reused || reusedMetadata.ID != metadata.ID {
		t.Fatalf("second prepare should reuse %q, got id=%q reused=%v", metadata.ID, reusedMetadata.ID, reused)
	}
	if data, readErr := os.ReadFile(marker); readErr != nil || string(data) != "kept" {
		t.Fatalf("cache hit reran or replaced setup output: data=%q err=%v", data, readErr)
	}

	overrides, activated, release, err := manager.Activate(metadata.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if activated.ID != metadata.ID || overrides["OPENKITTY_NATIVE_ENV_ID"] != metadata.ID {
		t.Fatalf("unexpected activation metadata: %#v %#v", activated, overrides)
	}
	wantValue := filepath.Join(manager.entryDir(metadata.ID), "payload", "value")
	if filepath.Clean(overrides["OPENKITTY_TEST_VALUE"]) != filepath.Clean(wantValue) {
		t.Fatalf("activation placeholder = %q, want %q", overrides["OPENKITTY_TEST_VALUE"], wantValue)
	}
	if !strings.Contains(overrides["PATH"], filepath.Join(manager.entryDir(metadata.ID), "payload", "bin")) {
		t.Fatalf("activation PATH does not contain cached bin directory: %q", overrides["PATH"])
	}
}

func TestNativeEnvironmentConcurrentPreparePublishesOnce(t *testing.T) {
	project := createNativeTestProject(t, "project", "module example.com/concurrent\n\ngo 1.22\n")
	manager := newNativeTestManager(project, t.TempDir())
	spec := NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   project,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("concurrent"),
	}
	type outcome struct {
		id     string
		reused bool
		err    error
	}
	const callers = 6
	results := make(chan outcome, callers)
	var start sync.WaitGroup
	start.Add(1)
	for index := 0; index < callers; index++ {
		go func() {
			start.Wait()
			metadata, reused, _, err := manager.Prepare(context.Background(), spec)
			results <- outcome{id: metadata.ID, reused: reused, err: err}
		}()
	}
	start.Done()

	firstID := ""
	created := 0
	for index := 0; index < callers; index++ {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if firstID == "" {
			firstID = result.id
		} else if result.id != firstID {
			t.Fatalf("concurrent callers received different ids: %q != %q", result.id, firstID)
		}
		if !result.reused {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("exactly one caller should create the environment, got %d", created)
	}
}

func TestNativeEnvironmentProjectScopeCannotAttachElsewhere(t *testing.T) {
	projectOne := createNativeTestProject(t, "one", "module example.com/one\n\ngo 1.22\n")
	projectTwo := createNativeTestProject(t, "two", "module example.com/one\n\ngo 1.22\n")
	manager := newNativeTestManager(projectOne, t.TempDir())
	metadata, _, output, err := manager.Prepare(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   projectOne,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("scoped"),
		Scope:        "project",
	})
	if err != nil {
		t.Fatalf("prepare: %v\n%s", err, output)
	}
	if _, _, _, err := manager.Activate(metadata.ID, projectTwo); err == nil ||
		!strings.Contains(err.Error(), "project-scoped") {
		t.Fatalf("expected project-scope rejection, got %v", err)
	}
}

func TestNativeEnvironmentPruneHonorsDryRunTTLAndActiveUse(t *testing.T) {
	project := createNativeTestProject(t, "project", "module example.com/prune\n\ngo 1.22\n")
	manager := newNativeTestManager(project, t.TempDir())
	metadata, _, output, err := manager.Prepare(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   project,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("old"),
	})
	if err != nil {
		t.Fatalf("prepare: %v\n%s", err, output)
	}
	old := time.Now().UTC().Add(-3 * time.Hour)
	metadata.LastUsedAt = old
	if err := manager.writeMetadata(manager.entryDir(metadata.ID), metadata); err != nil {
		t.Fatal(err)
	}
	manager.config.TTLHours = 1

	dryRun, err := manager.Prune(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dryRun.Removed) != 1 || dryRun.Removed[0].ID != metadata.ID {
		t.Fatalf("dry-run should report the expired entry: %#v", dryRun)
	}
	if _, err := os.Stat(manager.entryDir(metadata.ID)); err != nil {
		t.Fatalf("dry-run removed the environment: %v", err)
	}

	_, _, release, err := manager.Activate(metadata.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err = manager.loadMetadata(metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata.LastUsedAt = old
	if err := manager.writeMetadata(manager.entryDir(metadata.ID), metadata); err != nil {
		t.Fatal(err)
	}
	busyReport, err := manager.Prune(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(busyReport.Removed) != 0 || len(busyReport.SkippedBusy) == 0 {
		t.Fatalf("active environment should be skipped: %#v", busyReport)
	}
	release()

	removed, err := manager.Prune(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Removed) != 1 || removed.Removed[0].ID != metadata.ID {
		t.Fatalf("expired inactive environment was not removed: %#v", removed)
	}
	if _, err := os.Stat(manager.entryDir(metadata.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected environment directory to be removed, got %v", err)
	}
}

func TestNativeEnvironmentPruneEnforcesCapacityOldestFirst(t *testing.T) {
	project := createNativeTestProject(t, "project", "module example.com/capacity\n\ngo 1.22\n")
	manager := newNativeTestManager(project, t.TempDir())
	first, _, output, err := manager.Prepare(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   project,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("first"),
	})
	if err != nil {
		t.Fatalf("prepare first: %v\n%s", err, output)
	}
	second, _, output, err := manager.Prepare(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   project,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("second"),
	})
	if err != nil {
		t.Fatalf("prepare second: %v\n%s", err, output)
	}
	payload := make([]byte, 700*1024)
	if err := os.WriteFile(filepath.Join(manager.entryDir(first.ID), "payload", "large.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manager.entryDir(second.ID), "payload", "large.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first.LastUsedAt = now.Add(-2 * time.Hour)
	second.LastUsedAt = now.Add(-time.Hour)
	if err := manager.writeMetadata(manager.entryDir(first.ID), first); err != nil {
		t.Fatal(err)
	}
	if err := manager.writeMetadata(manager.entryDir(second.ID), second); err != nil {
		t.Fatal(err)
	}
	manager.config.MaxCacheMB = 1
	manager.config.TTLHours = 24

	report, err := manager.Prune(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Removed) != 1 || report.Removed[0].ID != first.ID ||
		report.Removed[0].Reason != "capacity" {
		t.Fatalf("capacity pruning should remove only the oldest entry: %#v", report)
	}
	if _, err := os.Stat(manager.entryDir(first.ID)); !os.IsNotExist(err) {
		t.Fatalf("oldest entry still exists: %v", err)
	}
	if _, err := os.Stat(manager.entryDir(second.ID)); err != nil {
		t.Fatalf("newer entry should remain: %v", err)
	}
}

func TestNativeEnvironmentPythonVenvIsBuiltOnceAndActivated(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("python runtime is unavailable")
	}
	project := filepath.Join(t.TempDir(), "python-project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "requirements.txt"), []byte("# intentionally empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := newNativeTestManager(project, t.TempDir())
	pathEntry := "bin"
	if runtime.GOOS == "windows" {
		pathEntry = "Scripts"
	}
	spec := NativeEnvironmentSpec{
		Ecosystem:       "python",
		ProjectDir:      project,
		Runtime:         python,
		DependencyFiles: []string{"requirements.txt"},
		SetupCommand:    "{runtime} -m venv {env_dir}",
		PathEntries:     []string{pathEntry},
		Environment:     map[string]string{"VIRTUAL_ENV": "{env_dir}"},
	}
	metadata, reused, output, err := manager.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("prepare python venv: %v\n%s", err, output)
	}
	if reused {
		t.Fatal("first Python prepare unexpectedly reused an environment")
	}
	second, reused, output, err := manager.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatalf("reuse python venv: %v\n%s", err, output)
	}
	if !reused || second.ID != metadata.ID {
		t.Fatalf("second Python prepare should reuse id=%q, got id=%q reused=%v", metadata.ID, second.ID, reused)
	}

	environment, _, release, err := manager.Activate(metadata.ID, project)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	command := `python -c "import os,sys; print(sys.prefix); print(os.environ['PYTHONPYCACHEPREFIX'])"`
	result, err := developmenttools.RunCommandWithEnvironment(project, command, 30*time.Second, environment)
	if err != nil {
		t.Fatalf("run cached Python: %v\n%s", err, result)
	}
	payloadDir := filepath.Join(manager.entryDir(metadata.ID), "payload")
	for _, want := range []string{payloadDir, filepath.Join(payloadDir, ".pycache")} {
		if !strings.Contains(strings.ToLower(result), strings.ToLower(want)) {
			t.Fatalf("cached Python output missing %q:\n%s", want, result)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "__pycache__")); !os.IsNotExist(err) {
		t.Fatalf("Python bytecode should not be written into the source tree: %v", err)
	}
}

func TestNativeExecutionDisabledDoesNotCreateCache(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "not-created")
	project := createNativeTestProject(t, "project", "module example.com/disabled\n\ngo 1.22\n")
	manager := NewNativeEnvironmentManager(project, NativeExecutionConfig{
		Enabled:  false,
		CacheDir: cacheDir,
	})
	_, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:    "go",
		ProjectDir:   project,
		Runtime:      "go",
		SetupCommand: nativeTestSetupCommand("disabled"),
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("disabled manager should not create its cache, stat error=%v", err)
	}
}

func TestNativeEnvironmentRejectsDependencySymlinkOutsideProject(t *testing.T) {
	project := createNativeTestProject(t, "project", "module example.com/symlink\n\ngo 1.22\n")
	outside := filepath.Join(t.TempDir(), "outside.lock")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "dependency.lock")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	manager := newNativeTestManager(project, t.TempDir())
	_, err := manager.Inspect(context.Background(), NativeEnvironmentSpec{
		Ecosystem:       "go",
		ProjectDir:      project,
		Runtime:         "go",
		DependencyFiles: []string{"dependency.lock"},
		SetupCommand:    nativeTestSetupCommand("symlink"),
	})
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("expected dependency symlink rejection, got %v", err)
	}
}

func newNativeTestManager(workspace, cacheDir string) *NativeEnvironmentManager {
	return NewNativeEnvironmentManager(workspace, NativeExecutionConfig{
		Enabled:               true,
		CacheDir:              cacheDir,
		MaxCacheMB:            64,
		TTLHours:              24,
		PrepareTimeoutSeconds: 30,
	})
}

func createNativeTestProject(t *testing.T, name, goMod string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func nativeTestSetupCommand(content string) string {
	if runtime.GOOS == "windows" {
		return "$target = Join-Path $env:OPENKITTY_NATIVE_ENV_DIR 'marker.txt'; " +
			"Set-Content -LiteralPath $target -Value '" + content + "' -NoNewline"
	}
	return "printf '%s' '" + content + "' > \"$OPENKITTY_NATIVE_ENV_DIR/marker.txt\""
}
