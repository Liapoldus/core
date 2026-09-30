package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	PluginReplicaServerCA    string
	Plugins                  []PluginInstanceConfig
}

// PluginReplicaConfig is one operator-declared plugin replica. Endpoint and
// expected identity are the normative registration values for the Core process
// lifetime: Core never derives, discovers or accepts them from a plugin.
type PluginReplicaConfig struct {
	ReplicaID            string
	Endpoint             string
	ExpectedPeerIdentity PeerIdentityConfig
}

// PluginInstanceConfig is one operator-declared plugin instance and the replicas
// that serve it.
type PluginInstanceConfig struct {
	InstanceID string
	Replicas   []PluginReplicaConfig
}

// PeerIdentityConfig is the expected mTLS identity of one replica, expressed the
// way the Plugin SDK compares it: an exact common name plus an optional exact
// uniform resource identifier.
type PeerIdentityConfig struct {
	CommonName                string
	UniformResourceIdentifier string
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
	Plugins                 []string `yaml:"plugins"`
	PluginReplica           []string `yaml:"pluginReplica"`
	PeerIdentity            []string `yaml:"peerIdentity"`
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
	if len(document.Content) == 0 || len(fieldLists.State) != 1 || len(fieldLists.PluginControlTLS) != 4 {
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
	pluginReplicaServerCA, err := optionalScalarValue(mappingValue(pluginControlTLS, fieldNameAt(fieldLists.PluginControlTLS, "replicaServerCA")))
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
	plugins, err := pluginInstances(mappingValue(root, fieldNameAt(loaded.Root, "plugins")), fieldLists)
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
		PluginReplicaServerCA:    resolveReference(path, pluginReplicaServerCA, loaded.SecretReference.FilePrefix),
		Plugins:                  plugins,
	}, nil
}

// pluginInstances decodes the operator-declared plugin registry. It is the only
// source of replica endpoints and expected identities, so a duplicated instance
// id, a duplicated replica id inside an instance, an empty replica set, a
// non-HTTPS endpoint or an identity that the Plugin SDK would refuse is rejected
// here rather than surfacing later as an unreachable replica at rollout time.
func pluginInstances(node *yaml.Node, fields bootstrapFieldLists) ([]PluginInstanceConfig, error) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, ErrInvalidDocument
	}
	if len(fields.Plugins) != 2 || len(fields.PluginReplica) != 3 || len(fields.PeerIdentity) != 2 {
		return nil, ErrInvalidDocument
	}
	instanceIDField, replicasField := fields.Plugins[0], fields.Plugins[1]
	replicaIDField, endpointField, identityField := fields.PluginReplica[0], fields.PluginReplica[1], fields.PluginReplica[2]
	commonNameField, identifierField := fields.PeerIdentity[0], fields.PeerIdentity[1]
	instances := make([]PluginInstanceConfig, 0, len(node.Content))
	seenInstances := make(map[string]bool, len(node.Content))
	for _, entry := range node.Content {
		if entry.Kind != yaml.MappingNode {
			return nil, ErrInvalidDocument
		}
		instanceID, err := scalarValue(mappingValue(entry, instanceIDField))
		if err != nil {
			return nil, err
		}
		if seenInstances[instanceID] {
			return nil, ErrInvalidDocument
		}
		seenInstances[instanceID] = true
		replicasNode := mappingValue(entry, replicasField)
		if replicasNode == nil || replicasNode.Kind != yaml.SequenceNode || len(replicasNode.Content) == 0 {
			return nil, ErrInvalidDocument
		}
		replicas := make([]PluginReplicaConfig, 0, len(replicasNode.Content))
		seenReplicas := make(map[string]bool, len(replicasNode.Content))
		for _, replicaNode := range replicasNode.Content {
			if replicaNode.Kind != yaml.MappingNode {
				return nil, ErrInvalidDocument
			}
			replicaID, err := scalarValue(mappingValue(replicaNode, replicaIDField))
			if err != nil {
				return nil, err
			}
			if seenReplicas[replicaID] {
				return nil, ErrInvalidDocument
			}
			seenReplicas[replicaID] = true
			endpoint, err := scalarValue(mappingValue(replicaNode, endpointField))
			if err != nil {
				return nil, err
			}
			// Reject a declared endpoint Core could not dial deterministically.
			// A rejected endpoint at load time is an operator typo they see
			// immediately, instead of a replica that silently never converges.
			if _, err := PluginEndpoint(endpoint); err != nil {
				return nil, ErrInvalidDocument
			}
			identityNode := mappingValue(replicaNode, identityField)
			if identityNode == nil || identityNode.Kind != yaml.MappingNode {
				return nil, ErrInvalidDocument
			}
			commonName, err := scalarValue(mappingValue(identityNode, commonNameField))
			if err != nil {
				return nil, err
			}
			identifier, err := optionalScalarValue(mappingValue(identityNode, identifierField))
			if err != nil {
				return nil, err
			}
			identity := PeerIdentityConfig{CommonName: commonName, UniformResourceIdentifier: identifier}
			if err := ValidPeerIdentity(identity.CommonName, identity.UniformResourceIdentifier); err != nil {
				return nil, ErrInvalidDocument
			}
			replicas = append(replicas, PluginReplicaConfig{
				ReplicaID: replicaID, Endpoint: endpoint, ExpectedPeerIdentity: identity,
			})
		}
		instances = append(instances, PluginInstanceConfig{InstanceID: instanceID, Replicas: replicas})
	}
	return instances, nil
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

// PluginEndpoint validates one declared replica origin and returns it together
// with the host Core must verify on the replica certificate.
//
// Core dials exactly this address and never learns it from a redirect, a
// manifest or a plugin advertisement, so the accepted shape is deliberately
// narrow: HTTPS, a host, no userinfo, no query, no fragment and no path. A
// query or fragment in particular is refused because the Plugin SDK control
// routes append their own path and query, and silently dropping either would
// send the operator somewhere they did not declare.
func PluginEndpoint(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, ErrInvalidDocument
	}
	return parsed, nil
}

// peerIdentityMaximumBytes bounds each declared identity field. The Plugin SDK
// applies the same limit when it builds the expected peer, so a value Core
// accepts is never silently wider than one the SDK would refuse.
const peerIdentityMaximumBytes = 256

// ValidPeerIdentity checks one operator-declared replica identity: an explicit
// common name plus an optional absolute URI.
//
// The rules are deliberately the same ones the Plugin SDK enforces, and they are
// enforced here as well so an operator sees the problem at `core serve` startup
// rather than as a replica that never converges. An empty field is never a
// wildcard: a declared identity either names the peer exactly or Core refuses it.
func ValidPeerIdentity(commonName, uniformResourceIdentifier string) error {
	if commonName == "" || len(commonName) > peerIdentityMaximumBytes ||
		strings.TrimSpace(commonName) != commonName {
		return ErrInvalidDocument
	}
	if uniformResourceIdentifier == "" {
		return nil
	}
	parsed, err := url.Parse(uniformResourceIdentifier)
	if err != nil || !parsed.IsAbs() || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasPrefix(parsed.Path, "/") ||
		len(uniformResourceIdentifier) > peerIdentityMaximumBytes {
		return ErrInvalidDocument
	}
	return nil
}
