package config

import (
	"net"
	"os"
	"strconv"
	"strings"

	assets "github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type TrafficControllerConfig struct {
	SchemaVersion     int
	SourcePath        string
	Listen            string
	Certificate       string
	Key               string
	ClientCA          string
	ClientCRLs        []string
	AllowedIdentities []PeerIdentityConfig
}

type trafficControllerFields struct {
	Root                []string `yaml:"root"`
	TLS                 []string `yaml:"tls"`
	AllowedIdentity     []string `yaml:"allowedIdentity"`
	FileReferencePrefix string   `yaml:"fileReferencePrefix"`
	CLIFlag             string   `yaml:"cliFlag"`
	ForbiddenCode       string   `yaml:"forbiddenCode"`
	UnavailableCode     string   `yaml:"unavailableCode"`
}

type TrafficControllerErrorCodes struct {
	Forbidden   string
	Unavailable string
}

func LoadTrafficControllerErrorCodes() (TrafficControllerErrorCodes, error) {
	return TrafficControllerErrorCodes{Forbidden: "forbidden", Unavailable: "management_unavailable"}, nil
}

func TrafficControllerCLIFlag() (string, error) {
	return "--traffic-controller-config", nil
}

// LoadTrafficController loads the isolated v2 controller listener settings.
// They intentionally do not extend the minimal v1 Core bootstrap document.
func LoadTrafficController(path string) (TrafficControllerConfig, error) {
	if path == "" {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	root := document.Content[0]
	fieldsBytes, err := assets.Contract(assets.TrafficControllerFields)
	if err != nil {
		return TrafficControllerConfig{}, err
	}
	var fields trafficControllerFields
	if err := yaml.Unmarshal(fieldsBytes, &fields); err != nil || len(fields.Root) != 4 || len(fields.TLS) != 4 || len(fields.AllowedIdentity) != 2 || fields.FileReferencePrefix == "" || fields.CLIFlag == "" || fields.ForbiddenCode == "" || fields.UnavailableCode == "" {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	if err := uniqueMappingKeys(root); err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	schemaBytes, err := assets.ContractV2("traffic-controller.schema.json")
	if err != nil {
		return TrafficControllerConfig{}, err
	}
	if err := validateDocumentSchema(root, schemaBytes); err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	if err := validateKnownFields(root, fields.Root); err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	versionNode := mappingValue(root, fields.Root[0])
	var schemaVersion int
	if versionNode == nil || versionNode.Decode(&schemaVersion) != nil || schemaVersion != 2 {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	listen, err := scalarValue(mappingValue(root, fields.Root[1]))
	if err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	if err := validListenAddress(listen); err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	tlsNode := mappingValue(root, fields.Root[2])
	if err := validateKnownFields(tlsNode, fields.TLS); err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	certificate, err := scalarValue(mappingValue(tlsNode, fields.TLS[0]))
	if err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	key, err := scalarValue(mappingValue(tlsNode, fields.TLS[1]))
	if err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	clientCA, err := scalarValue(mappingValue(tlsNode, fields.TLS[2]))
	if err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	crlNodes, err := optionalScalarSequence(mappingValue(tlsNode, fields.TLS[3]))
	if err != nil {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	identityNodes := mappingValue(root, fields.Root[3])
	if identityNodes == nil || identityNodes.Kind != yaml.SequenceNode || len(identityNodes.Content) == 0 {
		return TrafficControllerConfig{}, ErrInvalidDocument
	}
	identities := make([]PeerIdentityConfig, 0, len(identityNodes.Content))
	seen := make(map[string]struct{}, len(identityNodes.Content)*2)
	for _, identityNode := range identityNodes.Content {
		if err := validateKnownFields(identityNode, fields.AllowedIdentity); err != nil {
			return TrafficControllerConfig{}, ErrInvalidDocument
		}
		commonName, err := scalarValue(mappingValue(identityNode, fields.AllowedIdentity[0]))
		if err != nil {
			return TrafficControllerConfig{}, ErrInvalidDocument
		}
		uri, err := optionalScalarValue(mappingValue(identityNode, fields.AllowedIdentity[1]))
		if err != nil || ValidPeerIdentity(commonName, uri) != nil {
			return TrafficControllerConfig{}, ErrInvalidDocument
		}
		identity := PeerIdentityConfig{CommonName: commonName, UniformResourceIdentifier: uri}
		for _, candidate := range []string{identity.CommonName, identity.UniformResourceIdentifier} {
			if candidate == "" {
				continue
			}
			if _, exists := seen[candidate]; exists {
				return TrafficControllerConfig{}, ErrInvalidDocument
			}
			seen[candidate] = struct{}{}
		}
		identities = append(identities, identity)
	}
	return TrafficControllerConfig{
		SchemaVersion:     schemaVersion,
		SourcePath:        resolveRelative(path, path),
		Listen:            listen,
		Certificate:       resolveReference(path, certificate, fields.FileReferencePrefix),
		Key:               resolveReference(path, key, fields.FileReferencePrefix),
		ClientCA:          resolveReference(path, clientCA, fields.FileReferencePrefix),
		ClientCRLs:        resolveReferences(path, crlNodes, fields.FileReferencePrefix),
		AllowedIdentities: identities,
	}, nil
}

func validateKnownFields(node *yaml.Node, names []string) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return ErrInvalidDocument
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if !contains(names, node.Content[index].Value) {
			return ErrInvalidDocument
		}
	}
	return nil
}

func uniqueMappingKeys(node *yaml.Node) error {
	if node == nil {
		return ErrInvalidDocument
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index].Value
			if _, exists := seen[key]; exists {
				return ErrInvalidDocument
			}
			seen[key] = struct{}{}
			if err := uniqueMappingKeys(node.Content[index+1]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range node.Content {
		if err := uniqueMappingKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func validListenAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port < 1 || port > 65535 || strings.TrimSpace(host) != host || host == "" {
		return ErrInvalidDocument
	}
	return nil
}

func resolveReferences(path string, nodes []string, prefix string) []string {
	resolved := make([]string, len(nodes))
	for index, node := range nodes {
		resolved[index] = resolveReference(path, node, prefix)
	}
	return resolved
}
