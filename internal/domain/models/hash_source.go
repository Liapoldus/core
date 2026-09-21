package models

type HashSource uint8

const (
	HashSourceIP HashSource = iota + 1
	HashSourceHeader
	HashSourceCookie
	HashSourceQuery
)