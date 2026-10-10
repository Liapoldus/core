package api

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func randomID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(bytes)
}

func sliceValues(values any) []any {
	switch typed := values.(type) {
	case []any:
		return typed
	case []models.AuditRecord:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = typed[i]
		}
		return result
	default:
		return nil
	}
}
