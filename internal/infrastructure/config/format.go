package config

import (
	"bytes"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// FormatFile rewrites a single configuration file in place in canonical form:
// schema-validated, YAML aliases expanded, and mapping keys sorted recursively.
// The file is written only when the canonical form differs from the current
// contents, so formatting is idempotent.
func FormatFile(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}
	loaded, err := loadContractFile()
	if err != nil {
		return err
	}
	root := document.Content[0]
	for index := 0; index < len(root.Content); index += 2 {
		if !contains(loaded.Root, root.Content[index].Value) {
			return ErrUnknownField
		}
	}
	if err := validateSchema(root); err != nil {
		return err
	}
	encoded, err := yaml.Marshal(canonicalizeNode(root))
	if err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimRight(encoded, "\n"), bytes.TrimRight(contents, "\n")) {
		return nil
	}
	return os.WriteFile(path, encoded, 0o644)
}

func canonicalizeNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	node.Anchor = ""
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index < len(node.Content); index += 2 {
			node.Content[index+1] = canonicalizeNode(node.Content[index+1])
		}
		sortNodes(node)
		return node
	case yaml.SequenceNode:
		for index := range node.Content {
			node.Content[index] = canonicalizeNode(node.Content[index])
		}
		return node
	case yaml.AliasNode:
		if node.Alias == nil {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
		}
		return canonicalizeNode(cloneNode(node.Alias))
	default:
		return node
	}
}

func cloneNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Anchor = ""
	clone.Alias = nil
	if node.Content != nil {
		clone.Content = make([]*yaml.Node, len(node.Content))
		for index, child := range node.Content {
			clone.Content[index] = cloneNode(child)
		}
	}
	return &clone
}

func sortNodes(node *yaml.Node) {
	order := make([]int, len(node.Content)/2)
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(a, b int) bool {
		return node.Content[order[a]*2].Value < node.Content[order[b]*2].Value
	})
	sorted := make([]*yaml.Node, 0, len(node.Content))
	for _, original := range order {
		sorted = append(sorted, node.Content[original*2], node.Content[original*2+1])
	}
	node.Content = sorted
}
