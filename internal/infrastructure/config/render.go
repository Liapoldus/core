// Package config contains configuration compiler infrastructure adapters.
package config

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// effectiveDocument merges the supervised document graph (root first, then each
// include in collection order, later wins), drops the includes key, resolves
// variable substitutions on every scalar value except those inside secrets,
// and returns the resulting mapping root.
func effectiveDocument(path string) (*yaml.Node, error) {
	compiled, loaded, err := supervise(path)
	if err != nil {
		return nil, err
	}
	merged := mergeDocuments(compiled.documents, loaded)
	resolveScalars(merged, loaded, compiled.variables)
	return merged, nil
}

func mergeDocuments(documents []*yaml.Node, loaded contractFile) *yaml.Node {
	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var order []string
	values := map[string]*yaml.Node{}
	for _, document := range documents {
		for index := 0; index < len(document.Content); index += 2 {
			name := document.Content[index].Value
			if name == loaded.Includes {
				continue
			}
			next := document.Content[index+1]
			if _, exists := values[name]; !exists {
				order = append(order, name)
			}
			values[name] = next
		}
	}
	for _, name := range order {
		merged.Content = append(merged.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			values[name],
		)
	}
	return merged
}

func resolveScalars(node *yaml.Node, loaded contractFile, variables map[string]string) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index < len(node.Content); index += 2 {
			if node.Content[index].Value == loaded.Secrets {
				continue
			}
			resolveScalars(node.Content[index+1], loaded, variables)
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			resolveScalars(child, loaded, variables)
		}
	case yaml.ScalarNode:
		node.Value = substituteVariables(node.Value, variables, loaded.Substitution.Open, loaded.Substitution.Close)
	}
}

func substituteVariables(value string, variables map[string]string, open, close string) string {
	var buffer strings.Builder
	for remainder := value; ; {
		start := strings.Index(remainder, open)
		if start < 0 {
			buffer.WriteString(remainder)
			return buffer.String()
		}
		buffer.WriteString(remainder[:start])
		rest := remainder[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 {
			buffer.WriteString(rest)
			return buffer.String()
		}
		buffer.WriteString(variables[rest[:end]])
		remainder = rest[end+len(close):]
	}
}
