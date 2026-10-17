package storage

import "strings"

// Runtime statements use only the v15 service schema. Frozen pre-service query
// identifiers are translated for storage migration; this is not a public API
// alias or a second runtime model.
func configurationQueries() map[string]string {
	queries := make(map[string]string)
	for name, query := range historicalConfigurationQueries() {
		queries[strings.ReplaceAll(name, "plugin", "service")] = strings.ReplaceAll(query, "plugin", "service")
	}
	return queries
}
