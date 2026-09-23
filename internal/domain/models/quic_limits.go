package models

import "time"

type QUICLimits struct {
	MaxConnections int64
	MaxStreams     int64
	MaxPacketBytes uint64
	IdleTimeout    time.Duration
}
