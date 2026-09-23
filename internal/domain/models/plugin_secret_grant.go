package models

type PluginSecretGrant struct {
	Name                 string
	Purpose              string
	Domains              []string
	WildcardDomainPrefix string
}
