package models

type ServiceSecretGrant struct {
	Name                 string
	Purpose              string
	Domains              []string
	WildcardDomainPrefix string
}
