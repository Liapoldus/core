package models

type ConfigBundleTarget struct {
	Environment        string `json:"environment"`
	ExpectedGeneration int64  `json:"expectedGeneration,omitempty"`
}
