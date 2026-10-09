package models

// CoreSettingsSnapshot separates persisted desired state from the revision
// that passed validation during an explicit Core startup.
type CoreSettingsSnapshot struct {
	DesiredRevision   int64
	EffectiveRevision int64
	DesiredDocument   []byte
	EffectiveDocument []byte
	Pending           bool
}
