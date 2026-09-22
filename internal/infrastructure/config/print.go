package config

import (
	"gopkg.in/yaml.v3"
)

// PrintDocument returns the effective merged configuration rendered as YAML
// text: root and includes merged (later wins), includes key dropped, variables
// resolved, and secrets kept as env:/file: references only.
func PrintDocument(path string) ([]byte, error) {
	merged, err := effectiveDocument(path)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(merged)
}

// PrintJSON returns the effective merged configuration as JSON-compatible data
// for embedding in the CLI JSON envelope.
func PrintJSON(path string) (map[string]any, error) {
	merged, err := effectiveDocument(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := merged.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
