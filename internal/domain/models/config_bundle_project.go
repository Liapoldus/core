package models

type ConfigBundleProject struct {
	ID         string `json:"id"`
	Repository string `json:"repository,omitempty"`
	Revision   string `json:"revision"`
}
