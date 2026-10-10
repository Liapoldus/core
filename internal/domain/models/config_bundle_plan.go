package models

type ConfigBundlePlan struct {
	Valid    bool                   `json:"valid"`
	Digest   string                 `json:"digest"`
	Services []ConfigBundlePlanItem `json:"services"`
	Warnings []string               `json:"warnings,omitempty"`
}
