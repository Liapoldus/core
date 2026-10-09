package config

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"time"
)

// Settings is Core's operational document. It contains mount references, never
// secret values, database paths, or a static plugin endpoint registry.
type Settings struct {
	Management        ManagementSettings       `json:"management"`
	PluginControl     ControlSettings          `json:"pluginControl"`
	SecretRoot        string                   `json:"secretRoot"`
	TrafficController *TrafficControllerConfig `json:"trafficController,omitempty"`
}

type TLSReferences struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
	ClientCA    string `json:"clientCA,omitempty"`
}

type ManagementSettings struct {
	Listen         string        `json:"listen"`
	TLS            TLSReferences `json:"tls"`
	MaxBodyBytes   int64         `json:"maxBodyBytes"`
	HeaderTimeout  string        `json:"headerTimeout"`
	RequestTimeout string        `json:"requestTimeout"`
}

type ControlSettings struct {
	Listen            string        `json:"listen"`
	PublicURL         string        `json:"publicURL"`
	TLS               TLSReferences `json:"tls"`
	ReplicaClientCA   string        `json:"replicaClientCA"`
	ReplicaServerCA   string        `json:"replicaServerCA"`
	ReplicaClientCRLs []string      `json:"replicaClientCRLs,omitempty"`
	ReplicaServerCRLs []string      `json:"replicaServerCRLs,omitempty"`
}

func DecodeSettings(raw []byte) (Settings, error) {
	var result Settings
	if len(raw) == 0 || len(raw) > 262144 || rejectDuplicateKeys(raw) != nil {
		return result, ErrInvalidDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Validate() != nil {
		return Settings{}, ErrInvalidDocument
	}
	return result, nil
}

func (s Settings) Validate() error {
	if validListen(s.Management.Listen) != nil || validListen(s.PluginControl.Listen) != nil ||
		s.Management.MaxBodyBytes < 1 || s.Management.MaxBodyBytes > 16<<20 ||
		validDuration(s.Management.HeaderTimeout) != nil || validDuration(s.Management.RequestTimeout) != nil ||
		!filepath.IsAbs(s.SecretRoot) {
		return ErrInvalidDocument
	}
	if _, err := PluginEndpoint(s.PluginControl.PublicURL); err != nil {
		return ErrInvalidDocument
	}
	for _, path := range []string{s.Management.TLS.Certificate, s.Management.TLS.Key,
		s.PluginControl.TLS.Certificate, s.PluginControl.TLS.Key, s.PluginControl.ReplicaClientCA, s.PluginControl.ReplicaServerCA} {
		if !filepath.IsAbs(path) {
			return ErrInvalidDocument
		}
	}
	for _, path := range append(append([]string{s.Management.TLS.ClientCA}, s.PluginControl.ReplicaClientCRLs...), s.PluginControl.ReplicaServerCRLs...) {
		if path != "" && !filepath.IsAbs(path) {
			return ErrInvalidDocument
		}
	}
	if s.TrafficController != nil {
		controller := s.TrafficController
		if validListen(controller.Listen) != nil || controller.SchemaVersion != 2 ||
			!filepath.IsAbs(controller.Certificate) || !filepath.IsAbs(controller.Key) || !filepath.IsAbs(controller.ClientCA) ||
			len(controller.AllowedIdentities) == 0 {
			return ErrInvalidDocument
		}
		for _, identity := range controller.AllowedIdentities {
			if ValidPeerIdentity(identity.CommonName, identity.UniformResourceIdentifier) != nil {
				return ErrInvalidDocument
			}
		}
	}
	return nil
}

// Bootstrap gives runtime libraries typed parameters. StatePath comes only from
// the composition root's ENV bootstrap, not from the persisted document.
func (s Settings) Bootstrap(statePath string) BootstrapConfig {
	return BootstrapConfig{
		StatePath: statePath, SourcePath: filepath.Join(s.SecretRoot, "reference-base"),
		ManagementListen:      s.Management.Listen,
		ManagementCertificate: s.Management.TLS.Certificate, ManagementKey: s.Management.TLS.Key,
		ManagementClientCA: s.Management.TLS.ClientCA, ManagementMaxBodyBytes: s.Management.MaxBodyBytes,
		ManagementHeaderTimeout: s.Management.HeaderTimeout, ManagementRequestTimeout: s.Management.RequestTimeout,
		PluginControlListen: s.PluginControl.Listen, PluginControlPublicURL: s.PluginControl.PublicURL,
		PluginControlCertificate: s.PluginControl.TLS.Certificate, PluginControlKey: s.PluginControl.TLS.Key,
		PluginReplicaClientCA: s.PluginControl.ReplicaClientCA, PluginReplicaServerCA: s.PluginControl.ReplicaServerCA,
		PluginReplicaClientCRLs: append([]string(nil), s.PluginControl.ReplicaClientCRLs...),
		PluginReplicaServerCRLs: append([]string(nil), s.PluginControl.ReplicaServerCRLs...),
	}
}

func validListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || port == "" {
		return ErrInvalidDocument
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return ErrInvalidDocument
	}
	return nil
}

func validDuration(value string) error {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 || duration > 5*time.Minute {
		return ErrInvalidDocument
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return ErrInvalidDocument
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return ErrInvalidDocument
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return ErrInvalidDocument
				}
				seen[name] = true
				if value() != nil {
					return ErrInvalidDocument
				}
			}
		case '[':
			for decoder.More() {
				if value() != nil {
					return ErrInvalidDocument
				}
			}
		default:
			return ErrInvalidDocument
		}
		_, err = decoder.Token()
		if err != nil {
			return ErrInvalidDocument
		}
		return nil
	}
	if value() != nil {
		return ErrInvalidDocument
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidDocument
	}
	return nil
}
