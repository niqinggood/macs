package projectctx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

var ErrInstructionConflict = errors.New("instruction source changed; reload before editing")

// InstructionSnapshot reads a source already identified by the instruction
// loader. The HTTP host must obtain path from a stored server-side locator.
type InstructionSnapshot struct {
	Content  string
	Revision string
	Editable bool
	Missing  bool
}

var instructionEditLocks sync.Map

func instructionEditLock(path string) *sync.Mutex {
	lock, _ := instructionEditLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func instructionContentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func ReadInstructionSource(workspace, path string) (*InstructionSnapshot, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return &InstructionSnapshot{Missing: true}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 96<<10 {
		return nil, errors.New("instruction source is unavailable")
	}
	// A receipt must never become a way to read a replacement symlink target.
	resolved, err := ResolvePathThroughExistingAncestor(path)
	if err != nil || !SameWorkspace(filepath.Dir(resolved), filepath.Dir(path)) {
		return nil, errors.New("instruction source path changed")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("instruction source changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, (96<<10)+1))
	if err != nil || len(data) > 96<<10 || !utf8.Valid(data) {
		return nil, errors.New("instruction source exceeds the text limit")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	editable := PathWithinEqualFold(root, path)
	if editable {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		validated, err := ResolveLegacyWorkspacePath(root, relative)
		editable = err == nil && strings.EqualFold(filepath.Clean(validated), filepath.Clean(path))
	}
	return &InstructionSnapshot{Content: string(data), Revision: instructionContentDigest(string(data)), Editable: editable}, nil
}

// ChangeInstructionSource changes the original file, never the receipt. Files
// outside this workspace (including global instructions) remain read-only.
func ChangeInstructionSource(workspace, path, expected, action, value string) (*InstructionSnapshot, error) {
	lock := instructionEditLock(path)
	lock.Lock()
	defer lock.Unlock()
	current, err := ReadInstructionSource(workspace, path)
	if err != nil {
		return nil, err
	}
	if current.Missing || expected == "" || current.Revision != expected {
		return nil, ErrInstructionConflict
	}
	if !current.Editable {
		return nil, errors.New("instruction source is outside this workspace")
	}
	if action == "delete" {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		return &InstructionSnapshot{Missing: true}, nil
	}
	if action != "update" || len(value) > 96<<10 || !utf8.ValidString(value) {
		return nil, errors.New("invalid instruction source edit")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".macs-instruction-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		temp.Close()
		return nil, err
	}
	if _, err := temp.WriteString(value); err != nil {
		temp.Close()
		return nil, err
	}
	if err := temp.Close(); err != nil {
		return nil, err
	}
	// Check again after preparing the replacement, before committing it.
	current, err = ReadInstructionSource(workspace, path)
	if err != nil || current.Missing || !current.Editable || current.Revision != expected {
		return nil, ErrInstructionConflict
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return nil, err
	}
	return ReadInstructionSource(workspace, path)
}
