package config

// TrafficControllerConfig is persisted as part of Core settings in SQLite.
// Certificate and trust fields are references to mounted files, not secret data.
type TrafficControllerConfig struct {
	SchemaVersion     int                  `json:"schemaVersion"`
	Listen            string               `json:"listen"`
	Certificate       string               `json:"certificate"`
	Key               string               `json:"key"`
	ClientCA          string               `json:"clientCA"`
	ClientCRLs        []string             `json:"clientCRLs,omitempty"`
	AllowedIdentities []PeerIdentityConfig `json:"allowedIdentities"`
}

type TrafficControllerErrorCodes struct {
	Forbidden   string
	Unavailable string
}

func LoadTrafficControllerErrorCodes() (TrafficControllerErrorCodes, error) {
	return TrafficControllerErrorCodes{Forbidden: "forbidden", Unavailable: "management_unavailable"}, nil
}
