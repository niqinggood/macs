package app

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func architectureRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func TestCommandPackageIsThinEntrypoint(t *testing.T) {
	root := architectureRepositoryRoot(t)
	for _, command := range []string{"macs", "openkitty"} {
		t.Run(command, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(root, "cmd", command))
			if err != nil {
				t.Fatal(err)
			}
			var goFiles []string
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
					goFiles = append(goFiles, entry.Name())
				}
			}
			if len(goFiles) != 1 || goFiles[0] != "main.go" {
				t.Fatalf("cmd/%s must contain only main.go, got %v", command, goFiles)
			}
		})
	}
}

func TestStableInternalPackagesDoNotImportApplication(t *testing.T) {
	root := architectureRepositoryRoot(t)
	internal := filepath.Join(root, "internal")
	err := filepath.WalkDir(internal, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path == filepath.Join(internal, "app") {
			return filepath.SkipDir
		}
		// Black-box tests may import the public application API. The dependency
		// direction constraint applies to production packages.
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if importPath == "mac/internal/app" {
				t.Errorf("stable internal package imports application layer: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestArchiveAndReferenceTreesAreSeparateGoModules(t *testing.T) {
	root := architectureRepositoryRoot(t)
	boundaries := map[string]string{
		"23-old":    "openkitty.local/archive/v23old",
		"reference": "openkitty.local/reference/archive",
	}
	for directory, moduleName := range boundaries {
		path := filepath.Join(root, directory)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// Archives live outside the source tree. If restored for local
			// investigation, their module boundary must still be explicit.
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(path, "go.mod"))
		if err != nil {
			t.Fatalf("read module boundary %s: %v", directory, err)
		}
		if !strings.Contains(string(raw), "module "+moduleName) {
			t.Fatalf("%s does not declare %s", directory, moduleName)
		}
	}
}
