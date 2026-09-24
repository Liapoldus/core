package config

import (
	"os"
	"strings"

	assets "github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type BootstrapConfig struct {
	StatePath                string
	ArtifactsPath            string
	ManagementListen         string
	ManagementCertificate    string
	ManagementKey            string
	ManagementClientCA       string
	ManagementBearerVerifier string
	ManagementMaxBodyBytes   int64
	ManagementHeaderTimeout  string
	ManagementRequestTimeout string
	CaddyVariant             string
	CaddyBinary              string
	CaddyExpectedBuildID     string
}

type managementBootstrapOptions struct {
	MaxBodyBytes   int64
	HeaderTimeout  string
	RequestTimeout string
}

type bootstrapFieldLists struct {
	State                   []string `yaml:"state"`
	Artifacts               []string `yaml:"artifacts"`
	Management              []string `yaml:"management"`
	ManagementTLS           []string `yaml:"managementTLS"`
	ManagementRequestLimits []string `yaml:"managementRequestLimits"`
	Caddy                   []string `yaml:"caddy"`
}

// LoadBootstrap validates and decodes the bootstrap document without resolving
// secrets or interpreting paths. Callers receive external references, never
// secret contents.
func LoadBootstrap(path string) (BootstrapConfig, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return BootstrapConfig{}, err
	}
	loaded, err := loadContractFile()
	if err != nil {
		return BootstrapConfig{}, err
	}
	fieldLists, err := loadBootstrapFieldLists()
	if err != nil {
		return BootstrapConfig{}, err
	}
	if err := validateDocument(contents, loaded); err != nil {
		return BootstrapConfig{}, err
	}

	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return BootstrapConfig{}, err
	}
	if len(document.Content) == 0 || len(loaded.Root) != 4 {
		return BootstrapConfig{}, ErrInvalidDocument
	}
	root := document.Content[0]
	state := mappingValue(root, loaded.Root[0])
	artifacts := mappingValue(root, loaded.Root[1])
	management := mappingValue(root, loaded.Root[2])
	caddy := mappingValue(root, loaded.Root[3])
	statePath, err := onlyScalarValue(state, fieldLists.State)
	if err != nil {
		return BootstrapConfig{}, err
	}
	artifactsPath, err := onlyScalarValue(artifacts, fieldLists.Artifacts)
	if err != nil {
		return BootstrapConfig{}, err
	}

	managementListen, err := scalarValue(mappingValue(management, loaded.ManagementBootstrap.Listen))
	if err != nil {
		return BootstrapConfig{}, err
	}
	tls := mappingValue(management, loaded.ManagementBootstrap.TLS)
	if len(fieldLists.ManagementTLS) < 2 {
		return BootstrapConfig{}, ErrInvalidDocument
	}
	certificate, err := scalarValue(mappingValue(tls, fieldLists.ManagementTLS[0]))
	if err != nil {
		return BootstrapConfig{}, err
	}
	key, err := scalarValue(mappingValue(tls, fieldLists.ManagementTLS[1]))
	if err != nil {
		return BootstrapConfig{}, err
	}
	clientCA := ""
	if len(fieldLists.ManagementTLS) > 2 {
		clientCA, err = optionalScalarValue(mappingValue(tls, fieldLists.ManagementTLS[2]))
		if err != nil {
			return BootstrapConfig{}, err
		}
	}
	bearerVerifier, requestLimits, err := managementOptions(management, loaded, fieldLists)
	if err != nil {
		return BootstrapConfig{}, err
	}

	if len(fieldLists.Caddy) != 3 {
		return BootstrapConfig{}, ErrInvalidDocument
	}
	variant, err := scalarValue(mappingValue(caddy, fieldLists.Caddy[0]))
	if err != nil {
		return BootstrapConfig{}, err
	}
	binary, err := optionalScalarValue(mappingValue(caddy, fieldLists.Caddy[1]))
	if err != nil {
		return BootstrapConfig{}, err
	}
	buildID, err := optionalScalarValue(mappingValue(caddy, fieldLists.Caddy[2]))
	if err != nil {
		return BootstrapConfig{}, err
	}

	return BootstrapConfig{
		StatePath:                statePath,
		ArtifactsPath:            artifactsPath,
		ManagementListen:         managementListen,
		ManagementCertificate:    certificate,
		ManagementKey:            key,
		ManagementClientCA:       clientCA,
		ManagementBearerVerifier: bearerVerifier,
		ManagementMaxBodyBytes:   requestLimits.MaxBodyBytes,
		ManagementHeaderTimeout:  requestLimits.HeaderTimeout,
		ManagementRequestTimeout: requestLimits.RequestTimeout,
		CaddyVariant:             variant,
		CaddyBinary:              binary,
		CaddyExpectedBuildID:     buildID,
	}, nil
}

func loadBootstrapFieldLists() (bootstrapFieldLists, error) {
	contents, err := assets.Contract(assets.ConfigFields)
	if err != nil {
		return bootstrapFieldLists{}, err
	}
	var fields bootstrapFieldLists
	if err := yaml.Unmarshal(contents, &fields); err != nil {
		return bootstrapFieldLists{}, err
	}
	return fields, nil
}

func managementOptions(management *yaml.Node, loaded contractFile, fields bootstrapFieldLists) (string, managementBootstrapOptions, error) {
	var bearerVerifier string
	var requestLimits managementBootstrapOptions
	var err error
	for _, field := range fields.Management {
		if field == loaded.ManagementBootstrap.Listen || field == loaded.ManagementBootstrap.TLS {
			continue
		}
		value := mappingValue(management, field)
		if value == nil {
			continue
		}
		if value.Kind == yaml.ScalarNode && strings.HasPrefix(value.Value, loaded.SecretReference.FilePrefix) {
			bearerVerifier = value.Value
			continue
		}
		if value.Kind != yaml.MappingNode {
			continue
		}
		if len(fields.ManagementRequestLimits) != 3 {
			return "", requestLimits, ErrInvalidDocument
		}
		bodySize := mappingValue(value, fields.ManagementRequestLimits[0])
		if bodySize != nil {
			var parsed int64
			if err := bodySize.Decode(&parsed); err != nil {
				return "", requestLimits, err
			}
			requestLimits.MaxBodyBytes = parsed
		}
		requestLimits.HeaderTimeout, err = optionalScalarValue(mappingValue(value, fields.ManagementRequestLimits[1]))
		if err != nil {
			return "", requestLimits, err
		}
		requestLimits.RequestTimeout, err = optionalScalarValue(mappingValue(value, fields.ManagementRequestLimits[2]))
		if err != nil {
			return "", requestLimits, err
		}
	}
	if bearerVerifier == "" {
		return "", requestLimits, ErrInvalidDocument
	}
	return bearerVerifier, requestLimits, nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func onlyScalarValue(node *yaml.Node, fields []string) (string, error) {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) != 2 || len(fields) != 1 || node.Content[0].Value != fields[0] {
		return "", ErrInvalidDocument
	}
	return scalarValue(node.Content[1])
}

func scalarValue(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", ErrInvalidDocument
	}
	return node.Value, nil
}

func optionalScalarValue(node *yaml.Node) (string, error) {
	if node == nil {
		return "", nil
	}
	return scalarValue(node)
}
