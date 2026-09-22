package models

type ProxyHostMode uint8

const (
	ProxyHostPreserve ProxyHostMode = iota + 1
	ProxyHostUpstream
	ProxyHostValue
)
