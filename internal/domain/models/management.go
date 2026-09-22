package models

type Management struct {
	Listener        ManagementListener
	StaticToken     string
	ServiceAccounts []ServiceAccount
}
