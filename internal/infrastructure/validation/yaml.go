package validation

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Liapoldus/core/assets"
	"gopkg.in/yaml.v3"
)

type validationError uint8

func (value validationError) Error() string { return string(rune(value)) }

const (
	ErrUnknownField validationError = iota + 1
	ErrInvalidDocument
)

type contract struct {
	Root      []string
	Listener  []string
	Includes  string
	Listeners string
}

func Validate(path string) error {
	loaded, err := loadContract()
	if err != nil {
		return err
	}
	return validateFile(path, loaded, map[string]struct{}{})
}

func loadContract() (contract, error) {
	var loaded contract
	if err := yaml.Unmarshal(assets.ConfigFields(), &loaded); err != nil {
		return contract{}, err
	}
	return loaded, nil
}

func validateFile(path string, loaded contract, visited map[string]struct{}) error {
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
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if !contains(loaded.Root, key.Value) {
			return ErrUnknownField
		}
		switch key.Value {
		case loaded.Includes:
			if err := validateIncludes(abs, value, loaded, visited); err != nil {
				return err
			}
		case loaded.Listeners:
			if err := validateListeners(value, loaded); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateIncludes(parent string, node *yaml.Node, loaded contract, visited map[string]struct{}) error {
	if node.Kind != yaml.SequenceNode {
		return ErrInvalidDocument
	}
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return ErrInvalidDocument
		}
		if err := validateFile(filepath.Join(filepath.Dir(parent), item.Value), loaded, visited); err != nil {
			return err
		}
	}
	return nil
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
