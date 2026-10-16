package app

import (
	"testing"

	"mac/internal/toolcontract"
)

func TestPathsOverlapWindowsCaseInsensitive(t *testing.T) {
	if !toolcontract.PathsOverlap(`C:\Repo\File.txt`, `c:\repo`) {
		t.Fatal("expected Windows paths to overlap case-insensitively")
	}
}

func TestPathsOverlapWindowsSiblingPrefix(t *testing.T) {
	if toolcontract.PathsOverlap(`C:\RepoA`, `c:\repo`) {
		t.Fatal("sibling paths with shared text prefix should not overlap")
	}
}

func TestPathsOverlapMixedSeparators(t *testing.T) {
	if !toolcontract.PathsOverlap(`src\app`, `src/app/file.go`) {
		t.Fatal("expected mixed separator paths to overlap")
	}
}

func TestReadFileSamePathIsSafeParallel(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(NewReadFileTool(`C:\repo`))

	class := toolcontract.ClassifyToolParallel("read_file", map[string]interface{}{"path": `src\a.txt`}, reg, nil)
	if class != toolcontract.SafeParallel {
		t.Fatalf("read_file should classify as safe parallel before path-scoped checks, got %v", class)
	}
	if !toolcontract.CanRunConcurrently(
		ToolCall{ID: "1", Name: "read_file", Arguments: map[string]interface{}{"path": `src\a.txt`}},
		ToolCall{ID: "2", Name: "read_file", Arguments: map[string]interface{}{"path": `src\a.txt`}},
		reg, nil,
	) {
		t.Fatal("read_file calls for the same path should be able to run concurrently")
	}
}

func TestWriteFileAliasPathCanRunConcurrentlyWhenNonOverlapping(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(NewWriteFileTool(`C:\repo`))

	if !toolcontract.CanRunConcurrently(
		ToolCall{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"file": `src\a.txt`}},
		ToolCall{ID: "2", Name: "write_file", Arguments: map[string]interface{}{"file": `src\b.txt`}},
		reg, nil,
	) {
		t.Fatal("write_file calls using file alias on non-overlapping paths should be concurrent-safe")
	}
}

func TestWriteFileAliasPathOverlaps(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(NewWriteFileTool(`C:\repo`))

	if toolcontract.CanRunConcurrently(
		ToolCall{ID: "1", Name: "write_file", Arguments: map[string]interface{}{"file": `src`}},
		ToolCall{ID: "2", Name: "write_file", Arguments: map[string]interface{}{"file": `src\a.txt`}},
		reg, nil,
	) {
		t.Fatal("write_file calls using file alias on overlapping paths should not run concurrently")
	}
}
