package models

type ListenerLimits struct {
	Enabled     bool
	BodyBytes   uint64
	HeaderBytes int
}
