package api

import (
	"github.com/Liapoldus/core/internal/domain/models"
)

type pluginCookiePolicyInput struct {
	AllowedNames []string `json:"allowedNames"`
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
