package models

type WAFAction struct {
	Allow     bool
	Deny      *Deny
	Limit     string
	Plugin    *PluginTarget
	Problem   *Problem
}
