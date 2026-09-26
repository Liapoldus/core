package models

type PluginCookiePolicy struct {
	InstanceID   string   `json:"instanceId"`
	Capability   string   `json:"capability"`
	AllowedNames []string `json:"allowedNames"`
	Revision     int64    `json:"revision"`
}
