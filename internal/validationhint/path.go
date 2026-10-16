package validationhint

import (
	"path"
	"sort"
	"strings"

	"mac/internal/portablepath"
)

func SuggestedForPaths(paths []string) string {
	return SuggestedForPathsInWorkspace("", paths)
}

func SuggestedForPathsInWorkspace(workspace string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	seenExt := make(map[string]bool)
	goDirs := make(map[string]bool)
	for _, filePath := range paths {
		normalized := portablepath.Normalize(filePath)
		ext := strings.ToLower(path.Ext(normalized))
		if ext != "" {
			seenExt[ext] = true
		}
		if ext == ".go" {
			goDirs[GoTestPackagePath(workspace, normalized)] = true
		}
	}

	var suggestions []string
	if seenExt[".go"] {
		suggestions = append(suggestions, `run_tests command="go test ./..."`)
		for dir := range goDirs {
			if dir != "" && dir != "./..." {
				suggestions = append(suggestions, `run_tests command="go test `+dir+`"`)
			}
		}
	}
	if seenExt[".py"] {
		suggestions = append(suggestions, `run_tests command="pytest"`)
	}
	if seenExt[".js"] || seenExt[".jsx"] || seenExt[".ts"] || seenExt[".tsx"] || seenExt[".mjs"] {
		suggestions = append(suggestions, `run_tests command="npm test"`)
		suggestions = append(suggestions, `code_check action=build`)
	}
	if seenExt[".html"] || seenExt[".css"] {
		suggestions = append(suggestions, `open in browser or run the app build to verify UI/layout`)
	}
	if len(suggestions) == 0 {
		return ""
	}

	sort.Strings(suggestions)
	deduped := suggestions[:0]
	last := ""
	for _, s := range suggestions {
		if s == last {
			continue
		}
		deduped = append(deduped, s)
		last = s
	}
	return "\nSuggested validation:\n- " + strings.Join(deduped, "\n- ")
}

func GoTestPackagePath(workspace, filePath string) string {
	filePath = portablepath.Normalize(filePath)
	workspace = portablepath.Normalize(workspace)
	dir := path.Dir(filePath)
	if workspace != "" {
		if rel, ok := portablepath.RelativeTo(workspace, dir); ok {
			if rel == "." || rel == "" {
				return "."
			}
			return "./" + path.Clean(rel)
		}
	}
	if !portablepath.IsAbs(dir) {
		rel := portablepath.Normalize(dir)
		if rel == "." || rel == "" {
			return "."
		}
		return "./" + path.Clean(rel)
	}
	return "./..."
}
