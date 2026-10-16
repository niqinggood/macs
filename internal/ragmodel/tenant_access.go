package ragmodel

import (
	"fmt"
	"sort"

	"gorm.io/gorm"
)

// TenantKnowledgeBaseIDs resolves and validates the complete knowledge-base
// set for one already-normalized tenant partition.
func TenantKnowledgeBaseIDs(database *gorm.DB, tenantID string, requested []uint) ([]uint, error) {
	if database == nil {
		return nil, fmt.Errorf("knowledge database is unavailable")
	}

	if len(requested) == 0 {
		var ids []uint
		if err := database.Model(&Kb{}).
			Where("tenant_id = ?", tenantID).
			Order("id").
			Pluck("id", &ids).Error; err != nil {
			return nil, fmt.Errorf("resolve tenant knowledge bases: %w", err)
		}
		return ids, nil
	}

	unique := make(map[uint]struct{}, len(requested))
	ids := make([]uint, 0, len(requested))
	for _, id := range requested {
		if id == 0 {
			return nil, fmt.Errorf("knowledge base is not authorized for tenant")
		}
		if _, exists := unique[id]; exists {
			continue
		}
		unique[id] = struct{}{}
		ids = append(ids, id)
	}

	var matched []uint
	if err := database.Model(&Kb{}).
		Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Pluck("id", &matched).Error; err != nil {
		return nil, fmt.Errorf("validate tenant knowledge bases: %w", err)
	}
	if len(matched) != len(ids) {
		return nil, fmt.Errorf("knowledge base is not authorized for tenant")
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
