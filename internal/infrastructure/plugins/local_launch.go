package plugins

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type LocalInstanceRecord struct {
	ID           string
	Mode         string
	Revision     int64
	LaunchJSON   []byte
	SettingsJSON []byte
	ManifestJSON []byte
}

type localLaunchSettings struct {
	Binary string
}

type LocalRuntimeContract struct {
	LocalMode              string
	BinaryField            string
	MaximumSecretBytes     int64
	SecretGrantPurpose     string
	FileReferencePrefix    string
	LaunchSchema           []byte
	CallTimeout            string
	StartTimeout           string
	MaxConcurrentCalls     int
	RestartEnabled         bool
	RestartInitialBackoff  string
	RestartMaximumBackoff  string
	HealthProbeInterval    string
	HealthFailureThreshold int
	MemoryProbeInterval    string
	MemoryLimitBytes       uint64
	InvalidContract        string
	InvalidLaunch          string
}

type schemaIdentity struct {
	ID string `json:"$id"`
}

func BuildLocalInstances(records []LocalInstanceRecord, contract LocalRuntimeContract) (map[string]models.PluginInstance, error) {
	schema, err := compileLocalLaunchSchema(contract.LaunchSchema)
	if err != nil {
		return nil, errors.New(contract.InvalidContract)
	}
	defaults, err := pluginRuntimeDefaults(contract)
	if err != nil {
		return nil, errors.New(contract.InvalidContract)
	}
	instances := make(map[string]models.PluginInstance)
	for _, record := range records {
		if record.Mode != contract.LocalMode {
			continue
		}
		instance, err := buildLocalInstance(record, contract, schema, defaults)
		if err != nil {
			for _, prepared := range instances {
				clearConfigSecrets(prepared.ConfigGrantSecrets)
			}
			return nil, errors.New(contract.InvalidLaunch)
		}
		instances[record.ID] = instance
	}
	return instances, nil
}

type runtimeDefaults struct {
	callTimeout            time.Duration
	startTimeout           time.Duration
	maxConcurrentCalls     int
	restartEnabled         bool
	restartInitialBackoff  time.Duration
	restartMaximumBackoff  time.Duration
	healthProbeInterval    time.Duration
	healthFailureThreshold int
	memoryProbeInterval    time.Duration
	memoryLimitBytes       uint64
}

func pluginRuntimeDefaults(contract LocalRuntimeContract) (runtimeDefaults, error) {
	values := []string{
		contract.CallTimeout,
		contract.StartTimeout,
		contract.RestartInitialBackoff,
		contract.RestartMaximumBackoff,
		contract.HealthProbeInterval,
		contract.MemoryProbeInterval,
	}
	parsed := make([]time.Duration, len(values))
	for index, value := range values {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return runtimeDefaults{}, err
		}
		if duration <= 0 {
			return runtimeDefaults{}, errors.New(contract.InvalidContract)
		}
		parsed[index] = duration
	}
	return runtimeDefaults{
		callTimeout: parsed[0], startTimeout: parsed[1],
		maxConcurrentCalls:    contract.MaxConcurrentCalls,
		restartEnabled:        contract.RestartEnabled,
		restartInitialBackoff: parsed[2], restartMaximumBackoff: parsed[3],
		healthProbeInterval: parsed[4], healthFailureThreshold: contract.HealthFailureThreshold,
		memoryProbeInterval: parsed[5], memoryLimitBytes: contract.MemoryLimitBytes,
	}, nil
}

func buildLocalInstance(record LocalInstanceRecord, contract LocalRuntimeContract, schema *jsonschema.Schema, defaults runtimeDefaults) (models.PluginInstance, error) {
	if record.ID == "" || record.Revision < 1 || len(record.LaunchJSON) == 0 || !json.Valid(record.SettingsJSON) {
		return models.PluginInstance{}, errors.New(contract.InvalidLaunch)
	}
	var launchValue any
	if err := json.Unmarshal(record.LaunchJSON, &launchValue); err != nil || schema.Validate(launchValue) != nil {
		return models.PluginInstance{}, errors.New(contract.InvalidLaunch)
	}
	var launchFields map[string]json.RawMessage
	if err := json.Unmarshal(record.LaunchJSON, &launchFields); err != nil {
		return models.PluginInstance{}, errors.New(contract.InvalidLaunch)
	}
	var launch localLaunchSettings
	if err := json.Unmarshal(launchFields[contract.BinaryField], &launch.Binary); err != nil {
		return models.PluginInstance{}, errors.New(contract.InvalidLaunch)
	}
	manifest, err := ParseManifestInventory(record.ID, record.ManifestJSON)
	if err != nil {
		return models.PluginInstance{}, errors.New(contract.InvalidLaunch)
	}
	settings, configSecrets, err := preparePluginSettings(record.SettingsJSON, contract.FileReferencePrefix, contract.MaximumSecretBytes)
	if err != nil {
		return models.PluginInstance{}, errors.New(contract.InvalidLaunch)
	}
	return models.PluginInstance{
		Binary:       launch.Binary,
		Capabilities: manifest.Capabilities, Settings: settings,
		SettingsRevision:   strconv.FormatInt(record.Revision, 10),
		ConfigGrantSecrets: configSecrets, ConfigGrantPurpose: contract.SecretGrantPurpose,
		Timeout: defaults.callTimeout, StartTimeout: defaults.startTimeout,
		MaxConcurrentCalls:     defaults.maxConcurrentCalls,
		RestartEnabled:         defaults.restartEnabled,
		RestartInitialBackoff:  defaults.restartInitialBackoff,
		RestartMaximumBackoff:  defaults.restartMaximumBackoff,
		HealthProbeInterval:    defaults.healthProbeInterval,
		HealthFailureThreshold: defaults.healthFailureThreshold,
		MemoryProbeInterval:    defaults.memoryProbeInterval,
		MemoryLimitBytes:       defaults.memoryLimitBytes,
	}, nil
}

func preparePluginSettings(source []byte, prefix string, maximumBytes int64) ([]byte, map[string][]byte, error) {
	decoder := json.NewDecoder(strings.NewReader(string(source)))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, err
	}
	secrets := make(map[string][]byte)
	if err := replaceFileReferences(document, prefix, maximumBytes, secrets); err != nil {
		clearConfigSecrets(secrets)
		return nil, nil, err
	}
	contents, err := json.Marshal(document)
	if err != nil {
		clearConfigSecrets(secrets)
		return nil, nil, err
	}
	return contents, secrets, nil
}

func replaceFileReferences(value any, prefix string, maximumBytes int64, secrets map[string][]byte) error {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if err := replaceFileReferences(child, prefix, maximumBytes, secrets); err != nil {
				return err
			}
			if text, ok := child.(string); ok && strings.HasPrefix(text, prefix) {
				path := strings.TrimPrefix(text, prefix)
				secret, err := readPluginSecret(path, maximumBytes)
				if err != nil {
					return err
				}
				reference, err := newOpaqueReference(secrets)
				if err != nil {
					clear(secret)
					return err
				}
				secrets[reference] = secret
				current[key] = reference
			}
		}
	case []any:
		for index, child := range current {
			if err := replaceFileReferences(child, prefix, maximumBytes, secrets); err != nil {
				return err
			}
			if text, ok := child.(string); ok && strings.HasPrefix(text, prefix) {
				path := strings.TrimPrefix(text, prefix)
				secret, err := readPluginSecret(path, maximumBytes)
				if err != nil {
					return err
				}
				reference, err := newOpaqueReference(secrets)
				if err != nil {
					clear(secret)
					return err
				}
				secrets[reference] = secret
				current[index] = reference
			}
		}
	}
	return nil
}

func newOpaqueReference(existing map[string][]byte) (string, error) {
	var token [32]byte
	for attempt := 0; attempt < 4; attempt++ {
		if _, err := rand.Read(token[:]); err != nil {
			return "", err
		}
		reference := base64.RawURLEncoding.EncodeToString(token[:])
		if _, duplicate := existing[reference]; !duplicate {
			return reference, nil
		}
	}
	return "", os.ErrExist
}

func readPluginSecret(path string, maximumBytes int64) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) || maximumBytes < 1 {
		return nil, os.ErrInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumBytes {
		return nil, os.ErrInvalid
	}
	secret, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || int64(len(secret)) > maximumBytes || len(secret) == 0 {
		clear(secret)
		return nil, os.ErrInvalid
	}
	return secret, nil
}

func clearConfigSecrets(secrets map[string][]byte) {
	for _, secret := range secrets {
		clear(secret)
	}
}

func compileLocalLaunchSchema(contents []byte) (*jsonschema.Schema, error) {
	launchSchema, launchID, err := parseSchemaResource(contents)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(launchID, launchSchema); err != nil {
		return nil, err
	}
	return compiler.Compile(launchID)
}

func parseSchemaResource(contents []byte) (any, string, error) {
	var identity schemaIdentity
	if err := json.Unmarshal(contents, &identity); err != nil || identity.ID == "" {
		return nil, "", os.ErrInvalid
	}
	var resource any
	if err := json.Unmarshal(contents, &resource); err != nil {
		return nil, "", err
	}
	return resource, identity.ID, nil
}
