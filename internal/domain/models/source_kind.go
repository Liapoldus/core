package models

type SourceKind uint8

const (
	SourceDirectory SourceKind = iota + 1
	SourceRelease
)