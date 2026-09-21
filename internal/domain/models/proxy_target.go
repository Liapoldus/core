package models

type ProxyTarget struct {
	Upstream  string
	Host      ProxyHostMode
	HostValue string
}