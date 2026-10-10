package models

import "encoding/json"

type ConfigBundleManifest struct {
	SchemaVersion string                `json:"schemaVersion"`
	Digest        string                `json:"digest"`
	Services      []ConfigBundleService `json:"services"`
	Links         []json.RawMessage     `json:"links,omitempty"`
}
