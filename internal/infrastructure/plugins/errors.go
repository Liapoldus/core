package plugins

import "errors"

var (
	ErrProtocolViolation        = errors.New("plugin protocol violation")
	ErrPluginUnavailable        = errors.New("plugin unavailable")
	ErrPeerDirectoryUnavailable = errors.New("plugin peer directory unavailable")
)
