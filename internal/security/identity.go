package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

const personalPartitionBytes = 12

// BoundedIdentityValue trims whitespace and controls, then applies the legacy
// UTF-8 byte limit used for persisted execution identities.
func BoundedIdentityValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if limit > 0 && len(value) > limit {
		value = truncateIdentityBytes(value, limit)
	}
	return value
}

func ValidatePersonalSubjectID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	normalized := BoundedIdentityValue(value, 128)
	if normalized != value {
		return "", fmt.Errorf("user_id must be at most 128 UTF-8 bytes and contain no control characters")
	}
	return normalized, nil
}

// PersonalPartitionID derives an opaque path-safe partition from a tenant and
// effective execution subject.
func PersonalPartitionID(tenantID, subjectID, defaultTenantID string) string {
	tenantID = BoundedIdentityValue(tenantID, 64)
	if tenantID == "" {
		tenantID = defaultTenantID
	}
	subjectID = BoundedIdentityValue(subjectID, 128)
	sum := sha256.Sum256([]byte(tenantID + "\x00" + subjectID))
	return hex.EncodeToString(sum[:personalPartitionBytes])
}

func PersonalSessionPrefix(tenantID, subjectID, defaultTenantID string) string {
	return "ps_" + PersonalPartitionID(tenantID, subjectID, defaultTenantID) + "_"
}

func truncateIdentityBytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	suffix := "...[truncated]"
	if len(suffix) >= maxBytes {
		return suffix[:maxBytes]
	}
	cut := maxBytes - len(suffix)
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + suffix
}
