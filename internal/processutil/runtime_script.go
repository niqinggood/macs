package processutil

import (
	"os"
	"path/filepath"

	"mac/internal/fsutil"
)

// ResolveBuiltinRuntimeScript locates a bundled helper script in source and
// installed layouts.
func ResolveBuiltinRuntimeScript(name string) string {
	executable, _ := os.Executable()
	executableDirectory := filepath.Dir(executable)
	return fsutil.FirstExistingPath(
		filepath.Join("skills", name),
		filepath.Join("builtin", name),
		filepath.Join(executableDirectory, "skills", name),
		filepath.Join(executableDirectory, "builtin", name),
	)
}
