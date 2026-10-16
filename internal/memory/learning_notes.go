package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/projectctx"
)

type LearningNote struct {
	Path, Title, Description, Content string
	UpdatedAt                         time.Time
	Current                           CurrentSource
}

// Preserve the saved metadata while presenting/editing only the lesson body.
func learningNoteParts(raw string) (prefix, body, title, description string) {
	body = raw
	lines := strings.SplitAfter(raw, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return
	}
	position := len(lines[0])
	for _, line := range lines[1:] {
		position += len(line)
		text := strings.TrimSpace(line)
		if text == "---" {
			prefix, body = raw[:position], strings.TrimLeft(raw[position:], "\r\n")
			return
		}
		if strings.HasPrefix(text, "name:") {
			title = strings.Trim(strings.TrimSpace(strings.TrimPrefix(text, "name:")), "\"'")
		} else if strings.HasPrefix(text, "description:") {
			description = strings.Trim(strings.TrimSpace(strings.TrimPrefix(text, "description:")), "\"'")
		}
	}
	return "", raw, "", ""
}

func ReadLearningNote(workspace, path string) (*LearningNote, error) {
	if !projectctx.ContainsPath(workspace, path) || projectctx.EnsureResolvedPathInside(workspace, path) != nil {
		return nil, errors.New("learning note is outside this workspace")
	}
	snapshot, err := projectctx.ReadInstructionSource(workspace, path)
	if err != nil {
		return nil, err
	}
	note := &LearningNote{Path: path, Current: CurrentSource{Missing: snapshot.Missing}}
	if snapshot.Missing {
		return note, nil
	}
	_, body, title, description := learningNoteParts(snapshot.Content)
	note.Title, note.Description, note.Content = title, description, body
	if note.Title == "" {
		note.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if info, err := os.Stat(path); err == nil {
		note.UpdatedAt = info.ModTime()
	}
	note.Current = CurrentSource{Content: body, Revision: snapshot.Revision, Digest: promptContentDigest(body), Editable: snapshot.Editable, Deletable: snapshot.Editable}
	return note, nil
}

func ListLearningNotes(ctx context.Context, workspace, directory string, limit int) ([]LearningNote, error) {
	result := make([]LearningNote, 0)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !projectctx.ContainsPath(workspace, directory) || projectctx.EnsureResolvedPathInside(workspace, directory) != nil {
		return nil, errors.New("learning directory is outside this workspace")
	}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.ToLower(filepath.Ext(entry.Name())) != ".md" || strings.EqualFold(entry.Name(), "MEMORY.md") {
			continue
		}
		note, err := ReadLearningNote(workspace, filepath.Join(directory, entry.Name()))
		if err != nil || note.Current.Missing {
			continue
		}
		result = append(result, *note)
		if len(result) >= min(100, max(1, limit)) {
			break
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func ChangeLearningNote(workspace, path, expected, action, value string) (*CurrentSource, error) {
	if _, err := ReadLearningNote(workspace, path); err != nil {
		return nil, err
	}
	snapshot, err := projectctx.ReadInstructionSource(workspace, path)
	if err != nil {
		return nil, err
	}
	if snapshot.Missing || snapshot.Revision != expected {
		return nil, ErrPromptSourceConflict
	}
	prefix, _, _, _ := learningNoteParts(snapshot.Content)
	if action == "update" && prefix != "" {
		value = prefix + "\n" + value
	}
	if _, err := projectctx.ChangeInstructionSource(workspace, path, expected, action, value); err != nil {
		return nil, err
	}
	note, err := ReadLearningNote(workspace, path)
	if err != nil {
		return nil, err
	}
	return &note.Current, nil
}
