package config

import (
	"os"
	"path/filepath"

	assets "github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type BootstrapConfig struct {
	StatePath                string
	ManagementListen         string
	ManagementCertificate    string
	ManagementKey            string
	ManagementClientCA       string
	ManagementMaxBodyBytes   int64
	ManagementHeaderTimeout  string
	ManagementRequestTimeout string
	PluginControlListen      string
	PluginControlPublicURL   string
	PluginControlCertificate string
	PluginControlKey         string
	PluginReplicaClientCA    string
}

type managementBootstrapFields struct {
	Section  string `yaml:"section"`
	Listen   string `yaml:"listen"`
	TLS      string `yaml:"tls"`
	ClientCA string `yaml:"clientCA"`
}

type managementBootstrapOptions struct {
	MaxBodyBytes   int64
	HeaderTimeout  string
	RequestTimeout string
}

type bootstrapFieldLists struct {
	State                   []string `yaml:"state"`
	Management              []string `yaml:"management"`
	ManagementTLS           []string `yaml:"managementTLS"`
	ManagementRequestLimits []string `yaml:"managementRequestLimits"`
	PluginControl           []string `yaml:"pluginControl"`
	PluginControlTLS        []string `yaml:"pluginControlTLS"`
}

// LoadBootstrap validates and decodes bootstrap configuration. Relative paths
// and file references are resolved against the bootstrap document directory;
// callers receive paths, never secret contents.
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
	if err := ValidateYAML(string(contents)); err != nil {
		return BootstrapConfig{}, err
	}

	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return BootstrapConfig{}, err
	}
	if len(document.Content) == 0 || len(fieldLists.State) != 1 || len(fieldLists.PluginControlTLS) != 3 {
		return BootstrapConfig{}, ErrInvalidDocument
	}
	root := document.Content[0]
	state := mappingValue(root, fieldNameAt(loaded.Root, "state"))
	management := mappingValue(root, fieldNameAt(loaded.Root, "management"))
	statePath, err := onlyScalarValue(state, fieldLists.State)
	if err != nil {
		return BootstrapConfig{}, err
	}
	pluginControl := mappingValue(root, fieldNameAt(loaded.Root, "pluginControl"))
	pluginControlListen, err := optionalScalarValue(mappingValue(pluginControl, fieldNameAt(fieldLists.PluginControl, "listen")))
	if err != nil {
		return BootstrapConfig{}, err
	}
	pluginControlPublicURL, err := optionalScalarValue(mappingValue(pluginControl, fieldNameAt(fieldLists.PluginControl, "publicURL")))
	if err != nil {
		return BootstrapConfig{}, err
	}
	pluginControlTLS := mappingValue(pluginControl, fieldNameAt(fieldLists.PluginControl, "tls"))
	pluginControlCertificate, err := optionalScalarValue(mappingValue(pluginControlTLS, fieldNameAt(fieldLists.PluginControlTLS, "certificate")))
	if err != nil {
		return BootstrapConfig{}, err
	}
	pluginControlKey, err := optionalScalarValue(mappingValue(pluginControlTLS, fieldNameAt(fieldLists.PluginControlTLS, "key")))
	if err != nil {
		return BootstrapConfig{}, err
	}
	pluginReplicaClientCA, err := optionalScalarValue(mappingValue(pluginControlTLS, fieldNameAt(fieldLists.PluginControlTLS, "replicaClientCA")))
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
	requestLimits, err := managementOptions(management, loaded, fieldLists)
	if err != nil {
		return BootstrapConfig{}, err
	}

	return BootstrapConfig{
		StatePath:                resolveRelative(path, statePath),
		ManagementListen:         managementListen,
		ManagementCertificate:    resolveReference(path, certificate, loaded.SecretReference.FilePrefix),
		ManagementKey:            resolveReference(path, key, loaded.SecretReference.FilePrefix),
		ManagementClientCA:       resolveReference(path, clientCA, loaded.SecretReference.FilePrefix),
		ManagementMaxBodyBytes:   requestLimits.MaxBodyBytes,
		ManagementHeaderTimeout:  requestLimits.HeaderTimeout,
		ManagementRequestTimeout: requestLimits.RequestTimeout,
		PluginControlListen:      pluginControlListen,
		PluginControlPublicURL:   pluginControlPublicURL,
		PluginControlCertificate: resolveReference(path, pluginControlCertificate, loaded.SecretReference.FilePrefix),
		PluginControlKey:         resolveReference(path, pluginControlKey, loaded.SecretReference.FilePrefix),
		PluginReplicaClientCA:    resolveReference(path, pluginReplicaClientCA, loaded.SecretReference.FilePrefix),
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

func managementOptions(management *yaml.Node, loaded contractFile, fields bootstrapFieldLists) (managementBootstrapOptions, error) {
	var requestLimits managementBootstrapOptions
	for _, field := range fields.Management {
		if field == loaded.ManagementBootstrap.Listen || field == loaded.ManagementBootstrap.TLS {
			continue
		}
		value := mappingValue(management, field)
		if value == nil {
			continue
		}
		if value.Kind != yaml.MappingNode {
			continue
		}
		if len(fields.ManagementRequestLimits) != 3 {
			return requestLimits, ErrInvalidDocument
		}
		bodySize := mappingValue(value, fields.ManagementRequestLimits[0])
		if bodySize != nil {
			var parsed int64
			if err := bodySize.Decode(&parsed); err != nil {
				return requestLimits, err
			}
			requestLimits.MaxBodyBytes = parsed
		}
		var err error
		requestLimits.HeaderTimeout, err = optionalScalarValue(mappingValue(value, fields.ManagementRequestLimits[1]))
		if err != nil {
			return requestLimits, err
		}
		requestLimits.RequestTimeout, err = optionalScalarValue(mappingValue(value, fields.ManagementRequestLimits[2]))
		if err != nil {
			return requestLimits, err
		}
	}
	return requestLimits, nil
}

func resolveRelative(bootstrapPath, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	absoluteBootstrapPath, err := filepath.Abs(bootstrapPath)
	if err != nil {
		return filepath.Clean(filepath.Join(filepath.Dir(bootstrapPath), value))
	}
	return filepath.Clean(filepath.Join(filepath.Dir(absoluteBootstrapPath), value))
}

func resolveReference(bootstrapPath, value, prefix string) string {
	if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
		return resolveRelative(bootstrapPath, value[len(prefix):])
	}
	return value
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

func fieldNameAt(fields []string, name string) string {
	for _, field := range fields {
		if field == name {
			return field
		}
	}
	return ""
}
