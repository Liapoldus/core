package network

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

type l4Echo struct{}

func (l4Echo) L4(_ context.Context, _ string, request plugins.L4Request) (plugins.L4Response, error) {
	return plugins.L4Response{Payload: append([]byte("plugin:"), request.Payload...)}, nil
}

func TestTCPPluginTargetReceivesBoundedPayload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	route := models.Route{Plugin: &models.PluginTarget{Instance: "echo", Capability: "tcp.echo"}}
	done := make(chan error, 1)
	go func() {
		done <- ServeWithL4Capabilities(ctx, []models.Listener{{Type: "tcp", Address: address, Rules: []models.Route{route}}}, nil, nil, nil, time.Second, nil, map[string]L4CapabilityDispatcher{"echo": l4Echo{}})
	}()
	var conn net.Conn
	for attempt := 0; attempt < 50; attempt++ {
		conn, err = net.Dial("tcp", address)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 32)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "plugin:data" {
		t.Fatalf("payload = %q", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
}
