package network

import (
	"context"
	"sync/atomic"

	quic "github.com/quic-go/quic-go"
)

func quicConnectionLimit(maximum int64) func(context.Context, *quic.Conn) context.Context {
	var active atomic.Int64
	return func(ctx context.Context, connection *quic.Conn) context.Context {
		if active.Add(1) > maximum {
			active.Add(-1)
			_ = connection.CloseWithError(0, "")
			return ctx
		}
		context.AfterFunc(connection.Context(), func() { active.Add(-1) })
		return ctx
	}
}
