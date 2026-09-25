package config

import (
	"encoding/json"
	"errors"
	"net"
	"os"

	assets "github.com/Liapoldus/core"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

var (
	ErrUnknownField    = errors.New("unknown bootstrap field")
	ErrInvalidDocument = errors.New("invalid gateway bootstrap document")
)

type contractSecretReference struct {
	FilePrefix string `yaml:"filePrefix"`
}

type contractFile struct {
	Root                []string                  `yaml:"root"`
	ManagementBootstrap managementBootstrapFields `yaml:"managementBootstrap"`
	SecretReference     contractSecretReference   `yaml:"secretReference"`
}

// Validate validates a bootstrap document without reading or compiling any
// traffic configuration. Traffic is configured through Caddyfile revisions.
func Validate(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return ValidateYAML(string(contents))
}

// ValidateYAML applies the versioned bootstrap schema and network security
// invariants to an in-memory document.
func ValidateYAML(document string) error {
	loaded, err := loadContractFile()
	if err != nil {
		return err
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
		return err
	}
	if len(parsed.Content) == 0 || parsed.Content[0].Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}
	root := parsed.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		if !contains(loaded.Root, root.Content[index].Value) {
			return ErrUnknownField
		}
	}
	if err := validateSchema(root); err != nil {
		return err
	}
	return validateManagementSecurity(root, loaded.ManagementBootstrap)
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

func validateManagementSecurity(root *yaml.Node, fields managementBootstrapFields) error {
	management := mappingNode(root, fields.Section)
	listen, ok := fieldValue(management, fields.Listen)
	if !ok {
		return ErrInvalidDocument
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return ErrInvalidDocument
	}
	address := net.ParseIP(host)
	if address != nil && address.IsLoopback() {
		return nil
	}
	tls := mappingNode(management, fields.TLS)
	clientCA := mappingNode(tls, fields.ClientCA)
	if clientCA == nil || clientCA.Kind != yaml.ScalarNode || clientCA.Value == "" {
		return ErrInvalidDocument
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

func mappingNode(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == name {
			return node.Content[index+1]
		}
	}
	return nil
}

func fieldValue(node *yaml.Node, name string) (string, bool) {
	value := mappingNode(node, name)
	if value == nil || value.Kind != yaml.ScalarNode {
		return "", false
	}
	return value.Value, true
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
