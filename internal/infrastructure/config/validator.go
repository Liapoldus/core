package config

import (
	"encoding/json"
	"errors"
	"hash"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/assets"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

type validationError uint8

func (value validationError) Error() string { return string(rune(value)) }

const (
	ErrUnknownField validationError = iota + 1
	ErrInvalidDocument
	ErrUndefinedSite
)

type contractFile struct {
	Root         []string `yaml:"root"`
	Listener     []string `yaml:"listener"`
	Includes     string   `yaml:"includes"`
	Listeners    string   `yaml:"listeners"`
	Sites        string   `yaml:"sites"`
	Secrets      string   `yaml:"secrets"`
	Variables    string   `yaml:"variables"`
	Substitution struct {
		Open  string `yaml:"open"`
		Close string `yaml:"close"`
	} `yaml:"substitution"`
	Semantics struct {
		UnusedSite     string `yaml:"unusedSite"`
		OverriddenSite string `yaml:"overriddenSite"`
	} `yaml:"semantics"`
	Runtime runtimeWords `yaml:"runtime"`
}

type runtimeWords struct {
	HTTP      string
	Directory string
	Listener  struct {
		Type    string
		Address string
		Routes  string
	}
	Site struct {
		Source           string `yaml:"source"`
		Type             string `yaml:"type"`
		Root             string `yaml:"root"`
		Index            string `yaml:"index"`
		IndexDefault     string `yaml:"indexDefault"`
		ManifestFileName string `yaml:"manifestFileName"`
	}
	Route struct {
		When   string
		Then   string
		Path   string
		Prefix string
		Exact  string
		Regex  string
		Site   string
	}
}

type graph struct {
	variables map[string]string
	documents []*yaml.Node
	root      *yaml.Node
	hasher    hash.Hash
}

func supervise(path string) (*graph, contractFile, error) {
	loaded, err := loadContractFile()
	if err != nil {
		return nil, contractFile{}, err
	}
	compiled := graph{variables: map[string]string{}, hasher: newHasher()}
	if err := collectFile(path, loaded, map[string]struct{}{}, &compiled); err != nil {
		return nil, contractFile{}, err
	}
	for _, document := range compiled.documents {
		if err := validateVariables(document, compiled.variables, loaded); err != nil {
			return nil, contractFile{}, err
		}
	}
	return &compiled, loaded, nil
}

func Validate(path string) error {
	_, _, err := supervise(path)
	return err
}

func loadContractFile() (contractFile, error) {
	var loaded contractFile
	contents, err := assets.Contract(assets.ConfigFields)
	if err != nil {
		return contractFile{}, err
	}
	if err := yaml.Unmarshal(contents, &loaded); err != nil {
		return contractFile{}, err
	}
	return loaded, nil
}

func collectFile(path string, loaded contractFile, visited map[string]struct{}, compiled *graph) error {
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
	_, _ = compiled.hasher.Write(contents)
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}

	root := document.Content[0]
	if compiled.root == nil {
		compiled.root = root
	}
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
	return validateSchema(root)
}

func collectIncludes(parent string, node *yaml.Node, loaded contractFile, visited map[string]struct{}, compiled *graph) error {
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

func validateSchema(root *yaml.Node) error {
	var raw any
	if err := root.Decode(&raw); err != nil {
		return err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		return err
	}
	contents, err := assets.Contract(assets.GatewaySchema)
	if err != nil {
		return err
	}
	var schemaDocument struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(contents, &schemaDocument); err != nil {
		return err
	}
	var schemaValue any
	if err := json.Unmarshal(contents, &schemaValue); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaDocument.ID, schemaValue); err != nil {
		return err
	}
	schema, err := compiler.Compile(schemaDocument.ID)
	if err != nil {
		return err
	}
	return schema.Validate(instance)
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

func validateVariables(node *yaml.Node, variables map[string]string, loaded contractFile) error {
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

func validateScalar(value string, variables map[string]string, loaded contractFile) error {
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

func validateListeners(node *yaml.Node, loaded contractFile) error {
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
