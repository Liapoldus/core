package services

import "errors"

var (
	ErrProtocolViolation        = errors.New("service protocol violation")
	ErrServiceUnavailable       = errors.New("service unavailable")
	ErrPeerDirectoryUnavailable = errors.New("service peer directory unavailable")
)
