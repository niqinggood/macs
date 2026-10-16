package runtimeutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPromptDumpMaxFiles = 200
	maxPromptDumpMaxFiles     = 10000
)

func ResolvePromptDumpEnabled() bool {
	value, ok := os.LookupEnv("OPENKITTY_PROMPT_DUMP")
	if !ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

func ResolvePromptDumpMaxFiles() int {
	value := strings.TrimSpace(os.Getenv("OPENKITTY_PROMPT_DUMP_MAX_FILES"))
	if value == "" {
		return defaultPromptDumpMaxFiles
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return defaultPromptDumpMaxFiles
	}
	if limit > maxPromptDumpMaxFiles {
		return maxPromptDumpMaxFiles
	}
	return limit
}

// PersistPrompt writes a private JSON prompt dump and prunes the oldest files.
// Callers are responsible for serializing concurrent writes to the same directory.
func PersistPrompt(promptDir string, maxFiles, id int, messages []map[string]interface{}) {
	if strings.TrimSpace(promptDir) == "" || maxFiles <= 0 {
		return
	}

	data, err := json.Marshal(messages)
	if err != nil {
		return
	}
	if err := os.MkdirAll(promptDir, 0700); err != nil {
		return
	}
	_ = os.Chmod(promptDir, 0700)
	if err := os.WriteFile(filepath.Join(promptDir, strconv.Itoa(id)+".json"), data, 0600); err != nil {
		return
	}
	prunePromptDumps(promptDir, maxFiles)
}

type promptDumpFile struct {
	path    string
	id      int64
	modTime time.Time
}

func prunePromptDumps(promptDir string, maxFiles int) {
	if maxFiles <= 0 {
		return
	}
	entries, err := os.ReadDir(promptDir)
	if err != nil {
		return
	}
	files := make([]promptDumpFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), 10, 64)
		if err != nil {
			id = -1
		}
		files = append(files, promptDumpFile{
			path:    filepath.Join(promptDir, entry.Name()),
			id:      id,
			modTime: info.ModTime(),
		})
	}
	if len(files) <= maxFiles {
		return
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].id >= 0 && files[j].id >= 0 && files[i].id != files[j].id {
			return files[i].id < files[j].id
		}
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.Before(files[j].modTime)
		}
		return files[i].path < files[j].path
	})
	for _, file := range files[:len(files)-maxFiles] {
		_ = os.Remove(file.path)
	}
}
