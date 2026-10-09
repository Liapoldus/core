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

type ConfigBundleProject struct {
	ID         string `json:"id"`
	Repository string `json:"repository,omitempty"`
	Revision   string `json:"revision"`
}

type ConfigBundleManifest struct {
	SchemaVersion string                `json:"schemaVersion"`
	Digest        string                `json:"digest"`
	Services      []ConfigBundleService `json:"services"`
	Links         []json.RawMessage     `json:"links,omitempty"`
}

type ConfigBundleTarget struct {
	Environment        string `json:"environment"`
	ExpectedGeneration int64  `json:"expectedGeneration,omitempty"`
}

type ConfigBundleService struct {
	ID       string          `json:"id"`
	Settings json.RawMessage `json:"settings"`
}

type ConfigBundlePlan struct {
	Valid    bool                   `json:"valid"`
	Digest   string                 `json:"digest"`
	Services []ConfigBundlePlanItem `json:"services"`
	Warnings []string               `json:"warnings,omitempty"`
}

type ConfigBundlePlanItem struct {
	ID              string `json:"id"`
	CurrentRevision int64  `json:"currentRevision"`
	CurrentDigest   string `json:"currentDigest"`
	SchemaVersion   int64  `json:"schemaVersion"`
}

type ConfigBundleApply struct {
	OperationIDs []string `json:"operationIds"`
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
