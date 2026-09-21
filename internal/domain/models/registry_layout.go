package models

type RegistryLayout struct {
	Sites           string
	Releases        string
	Current         string
	Previous        string
	StagePrefix     string
	Manifest        string
	ManifestMissing string
	UnsafeSource    string
}