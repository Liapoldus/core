package models

import "encoding/json"

type ConfigBundleService struct {
	ID       string          `json:"id"`
	Settings json.RawMessage `json:"settings"`
}
