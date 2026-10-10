package models

type ConfigBundlePlanItem struct {
	ID              string `json:"id"`
	CurrentRevision int64  `json:"currentRevision"`
	CurrentDigest   string `json:"currentDigest"`
	SchemaVersion   int64  `json:"schemaVersion"`
}
