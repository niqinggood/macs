package memory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/projectctx"
)

const legacyPreferenceArchiveMaxBytes = 2 << 20

type legacyPreferenceArchiveItem struct {
	ContextItem
	Workspace string `json:"workspace"`
	TenantID  string `json:"tenant_id"`
	SubjectID string `json:"subject_id"`
	UserID    string `json:"user_id"`
	OwnerID   string `json:"owner_id"`
}

// ReadLegacyPreferenceArchive exposes only unscoped preferences from the fixed
// workspace file. It does not construct SCM (whose loader can migrate files),
// import entries, activate durable context or change automatic Recall.
func ReadLegacyPreferenceArchive(ctx context.Context, workspace string, limit int) ([]WorkingReference, error) {
	result := make([]WorkingReference, 0)
	if ctx == nil || strings.TrimSpace(workspace) == "" || limit < 1 || limit > 200 {
		return nil, errors.New("invalid legacy preference archive request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(workspace, ".openkitty", "context", "context.json")
	if !projectctx.ContainsPath(workspace, path) || projectctx.EnsureResolvedPathInside(workspace, path) != nil {
		return nil, errors.New("legacy preference archive is outside the workspace")
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > legacyPreferenceArchiveMaxBytes {
		return nil, errors.New("legacy preference archive is unavailable or too large")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("unable to open legacy preference archive")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > legacyPreferenceArchiveMaxBytes {
		return nil, errors.New("legacy preference archive is unavailable or too large")
	}
	data, err := io.ReadAll(io.LimitReader(file, legacyPreferenceArchiveMaxBytes+1))
	if err != nil || len(data) > legacyPreferenceArchiveMaxBytes {
		return nil, errors.New("unable to read bounded legacy preference archive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows map[string]json.RawMessage
	if json.Unmarshal(data, &rows) != nil {
		return nil, errors.New("invalid legacy preference archive")
	}
	for persistedKey, raw := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var item legacyPreferenceArchiveItem
		if string(raw) == "null" || json.Unmarshal(raw, &item) != nil || item.Category != "preference" || item.SessionID != "" || strings.TrimSpace(item.Value) == "" {
			continue
		}
		if item.Workspace != "" && !projectctx.SameWorkspace(item.Workspace, workspace) || item.TenantID != "" && item.TenantID != defaultExecutionTenantID || item.SubjectID != "" || item.UserID != "" || item.OwnerID != "" {
			continue
		}
		decodedSession, decodedKey, decodedDurable, composite := decodeContextStorageKey(persistedKey)
		if composite {
			// The key can retain a quarantined foreign session even when a row's
			// public session_id is missing or inconsistent. Never expose it here.
			if decodedSession != "" || item.Key != "" && item.Key != decodedKey || item.Durable != decodedDurable {
				continue
			}
			if item.Key == "" {
				item.Key = decodedKey
			}
		} else if strings.HasPrefix(persistedKey, contextStorageVersionPrefix) {
			continue
		}
		if item.Key == "" {
			item.Key = persistedKey
		}
		if len([]rune(item.Key)) > 256 || len(filterInternalExecutionContextItems([]*ContextItem{&item.ContextItem})) == 0 || shouldDropContextItemForWorkspace(&item.ContextItem, workspace) {
			continue
		}
		verified := false
		var created *time.Time
		if !item.CreatedAt.IsZero() {
			timestamp := item.CreatedAt
			created = &timestamp
		}
		originalID := ""
		if len(persistedKey) <= 2048 {
			originalID = persistedKey
		}
		result = append(result, WorkingReference{ID: "legacy-preference:" + memoryHash(canonicalContextStoreDir(workspace), persistedKey), SourceID: originalID, Key: item.Key,
			Value: truncateSessionRunes(item.Value, 1200), Scope: MemoryProject, Kind: "preference", SourceType: "legacy_preference", SourceLocation: ".openkitty/context/context.json",
			CreatedAt: created, UpdatedAt: item.UpdatedAt, ReadOnly: true, OwnershipVerified: &verified})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
