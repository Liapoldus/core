package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// ConfigBundleRequest is the API-only boundary used by the standalone CLI.
// Core validates the envelope, but service settings remain opaque JSON.
type ConfigBundleRequest struct {
	Project ConfigBundleProject  `json:"project"`
	Bundle  ConfigBundleManifest `json:"bundle"`
	Target  ConfigBundleTarget   `json:"target"`
}

func (request ConfigBundleRequest) Validate() error {
	if strings.TrimSpace(request.Project.ID) == "" || strings.TrimSpace(request.Project.Revision) == "" {
		return errors.New("project id and revision are required")
	}
	if strings.TrimSpace(request.Bundle.SchemaVersion) == "" || strings.TrimSpace(request.Bundle.Digest) == "" {
		return errors.New("bundle schemaVersion and digest are required")
	}
	if strings.TrimSpace(request.Target.Environment) == "" {
		return errors.New("target environment is required")
	}
	seen := make(map[string]struct{}, len(request.Bundle.Services))
	for _, service := range request.Bundle.Services {
		if strings.TrimSpace(service.ID) == "" || !jsonObject(service.Settings) {
			return errors.New("each service requires a valid settings document")
		}
		if _, ok := seen[service.ID]; ok {
			return errors.New("bundle contains duplicate service ids")
		}
		seen[service.ID] = struct{}{}
	}
	return nil
}

func jsonObject(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value map[string]json.RawMessage
	if err := decoder.Decode(&value); err != nil || value == nil {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}
