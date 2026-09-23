package models

type RegistryLayout struct {
	DefaultRoot       string
	Sites             string
	Releases          string
	Current           string
	Previous          string
	StagePrefix       string
	Manifest          string
	ManifestMissing   string
	UnsafeSource      string
	PublishLock       string
	PublishLockLease  string
	PublishLockFields RegistryLockFields
	PublishInProgress string
}
