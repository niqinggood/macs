package fsutil

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Exists reports whether path names an existing filesystem entry.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ExecutableDataDir returns the data directory beside the running executable.
func ExecutableDataDir() string {
	executable, err := os.Executable()
	if err != nil || executable == "" {
		return "data"
	}
	return filepath.Join(filepath.Dir(executable), "data")
}

// FindFileNamed returns the first regular file with name below directory.
func FindFileNamed(directory, name string) string {
	var found string
	_ = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() && strings.EqualFold(entry.Name(), name) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// Unzip extracts a ZIP archive below destination and rejects path traversal.
func Unzip(source, destination string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	for _, archived := range reader.File {
		target := filepath.Join(destination, filepath.FromSlash(archived.Name))
		relative, relErr := filepath.Rel(destination, target)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("unsafe ZIP path %q", archived.Name)
		}
		if archived.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := archived.Open()
		if err != nil {
			return err
		}
		mode := archived.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutputErr != nil {
			return closeOutputErr
		}
		if closeInputErr != nil {
			return closeInputErr
		}
	}
	return nil
}
