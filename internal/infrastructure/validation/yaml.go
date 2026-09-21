package validation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/internal/infrastructure/contracts"
	"gopkg.in/yaml.v3"
)

type validationError uint8

func (value validationError) Error() string { return string(rune(value)) }

const (
	ErrUnknownField validationError = iota + 1
	ErrInvalidDocument
)

type contract struct {
	Root         []string
	Listener     []string
	Includes     string
	Listeners    string
	Variables    string
	Substitution struct {
		Open  string
		Close string
	}
}

type graph struct {
	variables map[string]string
	documents []*yaml.Node
}

func Validate(path, contractPath string) error {
	loaded, err := loadContract(contractPath)
	if err != nil {
		return err
	}
	compiled := graph{variables: map[string]string{}}
	if err := collectFile(path, loaded, map[string]struct{}{}, &compiled); err != nil {
		return err
	}
	for _, document := range compiled.documents {
		if err := validateVariables(document, compiled.variables, loaded); err != nil {
			return err
		}
	}
	return nil
}

func loadContract(path string) (contract, error) {
	var loaded contract
	contents, err := contracts.Read(path)
	if err != nil {
		return contract{}, err
	}
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return contract{}, err
	}
	return loaded, nil
}

func collectFile(path string, loaded contract, visited map[string]struct{}, compiled *graph) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, exists := visited[abs]; exists {
		return ErrInvalidDocument
	}
	visited[abs] = struct{}{}
	defer delete(visited, abs)

	contents, err := os.ReadFile(abs)
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

	root := document.Content[0]
	compiled.documents = append(compiled.documents, root)
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if !contains(loaded.Root, key.Value) {
			return ErrUnknownField
		}
		switch key.Value {
		case loaded.Includes:
			if err := collectIncludes(abs, value, loaded, visited, compiled); err != nil {
				return err
			}
		case loaded.Listeners:
			if err := validateListeners(value, loaded); err != nil {
				return err
			}
		case loaded.Variables:
			if err := collectVariables(value, compiled.variables); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectIncludes(parent string, node *yaml.Node, loaded contract, visited map[string]struct{}, compiled *graph) error {
	if node.Kind != yaml.SequenceNode {
		return ErrInvalidDocument
	}
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return ErrInvalidDocument
		}
		if err := collectFile(filepath.Join(filepath.Dir(parent), item.Value), loaded, visited, compiled); err != nil {
			return err
		}
	}
	return nil
}

func collectVariables(node *yaml.Node, variables map[string]string) error {
	if node.Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}
	for index := 0; index < len(node.Content); index += 2 {
		name, value := node.Content[index], node.Content[index+1]
		if value.Kind != yaml.ScalarNode {
			return ErrInvalidDocument
		}
		if _, exists := variables[name.Value]; exists {
			return ErrInvalidDocument
		}
		variables[name.Value] = value.Value
	}
	return nil
}

func validateVariables(node *yaml.Node, variables map[string]string, loaded contract) error {
	if node.Kind == yaml.ScalarNode {
		return validateScalar(node.Value, variables, loaded)
	}
	for _, child := range node.Content {
		if err := validateVariables(child, variables, loaded); err != nil {
			return err
		}
	}
	return nil
}

func validateScalar(value string, variables map[string]string, loaded contract) error {
	for remainder := value; ; {
		start := strings.Index(remainder, loaded.Substitution.Open)
		if start < 0 {
			return nil
		}
		remainder = remainder[start+len(loaded.Substitution.Open):]
		end := strings.Index(remainder, loaded.Substitution.Close)
		if end < 0 {
			return ErrInvalidDocument
		}
		if _, exists := variables[remainder[:end]]; !exists {
			return ErrInvalidDocument
		}
		remainder = remainder[end+len(loaded.Substitution.Close):]
	}
}

func validateListeners(node *yaml.Node, loaded contract) error {
	if node.Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}
	for index := 1; index < len(node.Content); index += 2 {
		listener := node.Content[index]
		if listener.Kind != yaml.MappingNode {
			return ErrInvalidDocument
		}
		for field := 0; field < len(listener.Content); field += 2 {
			if !contains(loaded.Listener, listener.Content[field].Value) {
				return ErrUnknownField
			}
		}
	}
	return nil
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func IsUnknownField(err error) bool { return errors.Is(err, ErrUnknownField) }
