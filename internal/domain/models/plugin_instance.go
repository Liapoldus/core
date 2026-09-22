package models

import "time"

type PluginInstance struct {
	Binary       string
	Args         []string
	Env          []string
	Capabilities []string
	Settings     []byte
	Timeout      time.Duration
}
