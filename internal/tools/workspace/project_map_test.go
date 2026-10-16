package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMapDetectsGoProject(t *testing.T) {
	dir := t.TempDir()
	writeProjectMapTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")
	writeProjectMapTestFile(t, dir, "README.md", "# App\n")
	writeProjectMapTestFile(t, dir, filepath.Join("cmd", "app", "main.go"), "package main\nfunc main(){}\n")
	writeProjectMapTestFile(t, dir, filepath.Join("pkg", "calc.go"), "package pkg\n")
	writeProjectMapTestFile(t, dir, filepath.Join("pkg", "calc_test.go"), "package pkg\n")

	out, err := NewProjectMapTool(dir).Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Project Map",
		"Go",
		"go.mod",
		"cmd/app/main.go",
		"pkg/calc_test.go",
		"pkg/calc.go <-> pkg/calc_test.go",
		"go test ./...",
		"go build ./...",
		"Suggested Reads",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected project map to contain %q, got:\n%s", want, out)
		}
	}
}

func TestProjectMapDetectsNodeScriptsAndFrameworks(t *testing.T) {
	dir := t.TempDir()
	writeProjectMapTestFile(t, dir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	writeProjectMapTestFile(t, dir, "package.json", `{
  "scripts": {"test": "vitest", "build": "vite build", "dev": "vite"},
  "dependencies": {"react": "latest"},
  "devDependencies": {"vite": "latest", "vitest": "latest"}
}`)
	writeProjectMapTestFile(t, dir, filepath.Join("src", "main.tsx"), "export const app = 1\n")
	writeProjectMapTestFile(t, dir, filepath.Join("src", "main.test.tsx"), "test('x',()=>{})\n")
	writeProjectMapTestFile(t, dir, filepath.Join("node_modules", "ignored", "index.js"), "x\n")

	out, err := NewProjectMapTool(dir).Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Node/JS/TS", "React", "Vite", "Vitest", "pnpm", "npm test", "npm run build", "npm run dev", "src/main.tsx", "src/main.test.tsx", "src/main.tsx <-> src/main.test.tsx"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected project map to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "node_modules") {
		t.Fatalf("project map should skip node_modules, got:\n%s", out)
	}
}

func TestProjectMapDetectsTaskRunnerTargets(t *testing.T) {
	dir := t.TempDir()
	writeProjectMapTestFile(t, dir, "Makefile", "test:\n\tgo test ./...\nbuild:\n\tgo build ./...\ndev:\n\tgo run ./cmd/server\n")
	writeProjectMapTestFile(t, dir, "justfile", "verify:\n    go test ./...\nserve:\n    npm run dev\n")
	writeProjectMapTestFile(t, dir, "Taskfile.yml", "version: '3'\ntasks:\n  check:\n    cmds:\n      - go test ./...\n")

	out, err := NewProjectMapTool(dir).Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Makefile",
		"justfile",
		"Taskfile.yml",
		"make test",
		"make build",
		"make dev",
		"just verify",
		"just serve",
		"task check",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected project map to contain %q, got:\n%s", want, out)
		}
	}
}

func TestProjectMapDetectsDevelopmentAnchors(t *testing.T) {
	dir := t.TempDir()
	writeProjectMapTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")
	writeProjectMapTestFile(t, dir, "server.go", strings.Join([]string{
		"package main",
		"func wire() {",
		"  mux.HandleFunc(\"/api/items\", handleItems)",
		"  toolRegistry.Register(NewCLITool(\".\"))",
		"}",
	}, "\n"))
	writeProjectMapTestFile(t, dir, "app.py", strings.Join([]string{
		"from fastapi import APIRouter",
		"router = APIRouter()",
		"if __name__ == \"__main__\":",
		"    main()",
	}, "\n"))
	writeProjectMapTestFile(t, dir, filepath.Join("src", "routes.tsx"), strings.Join([]string{
		"export default function Routes() {",
		"  return <Route path=\"/\" />",
		"}",
	}, "\n"))

	out, err := NewProjectMapTool(dir).Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Development Anchors",
		"server.go:3 - HTTP route",
		"server.go:4 - registration point",
		"app.py:2 - HTTP route",
		"app.py:3 - script entry",
		"src/routes.tsx:1 - exported component/function",
		"src/routes.tsx:2 - frontend route",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected project map to contain %q, got:\n%s", want, out)
		}
	}
}

func TestProjectMapUsesRuntimeCacheAndRefreshes(t *testing.T) {
	dir := t.TempDir()
	writeProjectMapTestFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.22\n")
	tool := NewProjectMapTool(dir)

	first, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "go.mod") {
		t.Fatalf("first map should include go.mod, got:\n%s", first)
	}
	if _, err := os.Stat(projectMapCachePath(dir)); err != nil {
		t.Fatalf("expected project map cache to be written: %v", err)
	}

	writeProjectMapTestFile(t, dir, filepath.Join("cmd", "newapp", "main.go"), "package main\nfunc main(){}\n")
	cached, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cached, "Cache: hit") {
		t.Fatalf("expected cache hit marker, got:\n%s", cached)
	}
	if strings.Contains(cached, "cmd/newapp/main.go") {
		t.Fatalf("cache hit should not include newly written file before refresh:\n%s", cached)
	}

	refreshed, err := tool.Execute(context.Background(), map[string]interface{}{"refresh": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refreshed, "cmd/newapp/main.go") {
		t.Fatalf("refresh should include new entry point, got:\n%s", refreshed)
	}
	if strings.Contains(refreshed, "Cache: hit") {
		t.Fatalf("refresh should not be reported as cache hit:\n%s", refreshed)
	}
}

func writeProjectMapTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
