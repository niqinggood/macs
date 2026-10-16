package skills

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	rootassets "mac"
	"mac/internal/fsutil"
	"mac/internal/projectctx"
)

const defaultUserSkillStateFile = ".openkitty-default-skills.json"

func NormalizeName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." || len(name) > 128 || filepath.Base(name) != name ||
		strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", false
	}
	for _, character := range name {
		if unicode.IsControl(character) || !(unicode.IsLetter(character) || unicode.IsNumber(character) || strings.ContainsRune("-_. ", character)) {
			return "", false
		}
	}
	upperBase := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	switch upperBase {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "", false
	}
	return name, true
}

// defaultUserSkillsFS contains optional, user-visible skills. Unlike builtin
// skills, these are offered once beside the executable so users can inspect,
// edit, or delete them from the normal Skills UI.
var defaultUserSkillsFS = rootassets.DefaultSkillsFS

var defaultUserSkillsMu sync.Mutex

type defaultUserSkillRecord struct {
	OfferedDigest   string `json:"offered_digest"`
	InstalledDigest string `json:"installed_digest,omitempty"`
	Managed         bool   `json:"managed"`
}

type defaultUserSkillState struct {
	Version int                               `json:"version"`
	Skills  map[string]defaultUserSkillRecord `json:"skills"`
}

type DefaultUserSkillSeedResult struct {
	Installed         []string
	Updated           []string
	Preserved         []string
	PreviouslyRemoved []string
}

// SeedDefaultUserSkills offers each embedded default skill to this executable's
// user skill directory. A state file records the offer:
//   - a user-deleted skill stays deleted on later startups;
//   - a user-edited skill is never overwritten;
//   - an untouched seeded skill may receive a newer embedded version.
func SeedDefaultUserSkills(skillsDir string) (DefaultUserSkillSeedResult, error) {
	defaultUserSkillsMu.Lock()
	defer defaultUserSkillsMu.Unlock()
	return seedDefaultUserSkillsFromFS(defaultUserSkillsFS, "default-skills", skillsDir)
}

func seedDefaultUserSkillsFromFS(
	sourceFS fs.FS,
	sourceRoot string,
	skillsDir string,
) (DefaultUserSkillSeedResult, error) {
	var result DefaultUserSkillSeedResult
	if sourceFS == nil {
		return result, fmt.Errorf("default skill filesystem is nil")
	}
	if strings.TrimSpace(skillsDir) == "" {
		return result, fmt.Errorf("default skill target directory is empty")
	}
	targetRoot, err := filepath.Abs(filepath.Clean(skillsDir))
	if err != nil {
		return result, fmt.Errorf("resolve default skill target: %w", err)
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return result, fmt.Errorf("create default skill target: %w", err)
	}

	statePath := filepath.Join(targetRoot, defaultUserSkillStateFile)
	state, stateExists, err := loadDefaultUserSkillState(statePath)
	if err != nil {
		return result, err
	}
	if state.Skills == nil {
		state.Skills = make(map[string]defaultUserSkillRecord)
	}

	entries, err := fs.ReadDir(sourceFS, sourceRoot)
	if err != nil {
		return result, fmt.Errorf("read embedded default skills: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	stateChanged := !stateExists
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name, ok := NormalizeName(entry.Name())
		if !ok || name != entry.Name() {
			return result, fmt.Errorf("invalid embedded default skill name %q", entry.Name())
		}
		sourceDir := filepath.ToSlash(filepath.Join(sourceRoot, name))
		if _, err := fs.Stat(sourceFS, filepath.ToSlash(filepath.Join(sourceDir, "SKILL.md"))); err != nil {
			return result, fmt.Errorf("embedded default skill %q has no SKILL.md: %w", name, err)
		}
		offeredDigest, err := BundleDigest(sourceFS, sourceDir)
		if err != nil {
			return result, fmt.Errorf("hash embedded default skill %q: %w", name, err)
		}

		target := filepath.Join(targetRoot, name)
		if err := projectctx.EnsureResolvedPathInside(targetRoot, target); err != nil {
			return result, err
		}
		record, wasOffered := state.Skills[name]
		info, statErr := os.Lstat(target)
		switch {
		case os.IsNotExist(statErr):
			if wasOffered {
				// The skill was offered before and is now absent. Treat that as
				// the user's deletion choice, even if the embedded copy changed.
				record.OfferedDigest = offeredDigest
				state.Skills[name] = record
				stateChanged = true
				result.PreviouslyRemoved = append(result.PreviouslyRemoved, name)
				continue
			}
			if err := installDefaultUserSkill(sourceFS, sourceDir, targetRoot, target); err != nil {
				return result, fmt.Errorf("install default skill %q: %w", name, err)
			}
			state.Skills[name] = defaultUserSkillRecord{
				OfferedDigest: offeredDigest, InstalledDigest: offeredDigest, Managed: true,
			}
			stateChanged = true
			result.Installed = append(result.Installed, name)

		case statErr != nil:
			return result, fmt.Errorf("inspect default skill target %q: %w", target, statErr)

		case !info.IsDir():
			// Preserve collisions instead of replacing an arbitrary user file.
			state.Skills[name] = defaultUserSkillRecord{OfferedDigest: offeredDigest, Managed: false}
			stateChanged = true
			result.Preserved = append(result.Preserved, name)

		case !wasOffered:
			// A pre-existing user skill with the same name always wins.
			state.Skills[name] = defaultUserSkillRecord{OfferedDigest: offeredDigest, Managed: false}
			stateChanged = true
			result.Preserved = append(result.Preserved, name)

		case !record.Managed:
			if record.OfferedDigest != offeredDigest {
				record.OfferedDigest = offeredDigest
				state.Skills[name] = record
				stateChanged = true
			}
			result.Preserved = append(result.Preserved, name)

		default:
			currentDigest, digestErr := BundleDigest(os.DirFS(target), ".")
			if digestErr != nil {
				return result, fmt.Errorf("hash installed default skill %q: %w", name, digestErr)
			}
			if currentDigest != record.InstalledDigest {
				// The seeded copy was edited after installation. Relinquish
				// management permanently and preserve the user's version.
				record.Managed = false
				record.OfferedDigest = offeredDigest
				state.Skills[name] = record
				stateChanged = true
				result.Preserved = append(result.Preserved, name)
				continue
			}
			if record.OfferedDigest == offeredDigest {
				continue
			}
			if err := installDefaultUserSkill(sourceFS, sourceDir, targetRoot, target); err != nil {
				return result, fmt.Errorf("update default skill %q: %w", name, err)
			}
			record.OfferedDigest = offeredDigest
			record.InstalledDigest = offeredDigest
			state.Skills[name] = record
			stateChanged = true
			result.Updated = append(result.Updated, name)
		}
	}

	if stateChanged {
		state.Version = 1
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return result, fmt.Errorf("encode default skill state: %w", err)
		}
		data = append(data, '\n')
		if err := fsutil.WriteAtomic(statePath, data, 0o600); err != nil {
			return result, fmt.Errorf("write default skill state: %w", err)
		}
	}
	return result, nil
}

func loadDefaultUserSkillState(path string) (defaultUserSkillState, bool, error) {
	state := defaultUserSkillState{Version: 1, Skills: make(map[string]defaultUserSkillRecord)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, false, nil
	}
	if err != nil {
		return state, false, fmt.Errorf("read default skill state: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		// A corrupt state file must not cause previously deleted skills to be
		// reinstalled or user content to be overwritten.
		return defaultUserSkillState{}, true, fmt.Errorf("parse default skill state: %w", err)
	}
	if state.Skills == nil {
		state.Skills = make(map[string]defaultUserSkillRecord)
	}
	return state, true, nil
}

func BundleDigest(fsys fs.FS, root string) (string, error) {
	hasher := sha256.New()
	root = filepath.ToSlash(root)
	err := fs.WalkDir(fsys, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(path))
		if err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, filepath.ToSlash(path))
		if err != nil {
			return err
		}
		_, _ = hasher.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(data)
		_, _ = hasher.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func installDefaultUserSkill(
	sourceFS fs.FS,
	sourceDir string,
	targetRoot string,
	target string,
) error {
	if err := projectctx.EnsureResolvedPathInside(targetRoot, target); err != nil {
		return err
	}
	name := filepath.Base(target)
	staging, err := os.MkdirTemp(targetRoot, "."+name+".seeding-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := projectctx.EnsureResolvedPathInside(targetRoot, staging); err != nil {
		return err
	}
	if err := MaterializeFSDir(sourceFS, sourceDir, staging); err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(staging, "SKILL.md")); err != nil || info.IsDir() {
		return fmt.Errorf("staged skill is missing SKILL.md")
	}

	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return renameDefaultUserSkillPath(staging, target)
	} else if err != nil {
		return err
	}

	backup, err := os.MkdirTemp(targetRoot, "."+name+".backup-")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := projectctx.EnsureResolvedPathInside(targetRoot, backup); err != nil {
		return err
	}
	if err := renameDefaultUserSkillPath(target, backup); err != nil {
		return err
	}
	if err := renameDefaultUserSkillPath(staging, target); err != nil {
		_ = renameDefaultUserSkillPath(backup, target)
		return err
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove replaced default skill backup: %w", err)
	}
	return nil
}

func renameDefaultUserSkillPath(source, target string) error {
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 8
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := os.Rename(source, target); err == nil {
			return nil
		} else {
			lastErr = err
			if runtime.GOOS != "windows" || !errors.Is(err, os.ErrPermission) {
				return err
			}
		}
		if attempt+1 < attempts {
			time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
		}
	}
	return lastErr
}
