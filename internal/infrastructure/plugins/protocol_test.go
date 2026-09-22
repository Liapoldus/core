package plugins

import (
	"context"
	"net"
	"testing"

	"github.com/Liapoldus/pluginprotocol/framing"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/proto"
)

func TestHandshakeUsesManifestHealthAndConfigApply(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			frame, err := framing.Decode(right)
			if err != nil {
				done <- err
				return
			}
			req := new(pluginv1.Envelope)
			if err := proto.Unmarshal(frame.GetPayload(), req); err != nil {
				done <- err
				return
			}
			var payload []byte
			switch req.GetMethod() {
			case "manifest":
				payload, _ = proto.Marshal(&pluginv1.Manifest{Name: "forms", ProtocolVersion: "liapoldus.plugin.v1", Capabilities: []string{"admin.ui"}})
			case "health":
				payload, _ = proto.Marshal(&pluginv1.Health{Ready: true})
			case "config.apply":
				if string(req.GetPayload()) != "settings" {
					done <- ErrProtocolViolation
					return
				}
				payload, _ = proto.Marshal(&pluginv1.ConfigApplyResult{Applied: true})
			}
			encoded, _ := proto.Marshal(&pluginv1.Envelope{Payload: payload})
			if err := framing.Encode(right, &pluginv1.Frame{Kind: pluginv1.FrameKind_CALL_RESULT, RequestId: frame.GetRequestId(), Payload: encoded}); err != nil {
				done <- err
				return
			}
		}
	}()
	client := NewClient(left, 0)
	handshake, err := client.Handshake(context.Background(), "liapoldus.plugin.v1", []byte("settings"))
	if err != nil {
		t.Fatal(err)
	}
	if handshake.Manifest.GetName() != "forms" {
		t.Fatalf("unexpected manifest: %s", handshake.Manifest.GetName())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsMismatchedResponse(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go func() {
		frame, _ := framing.Decode(right)
		_ = framing.Encode(right, &pluginv1.Frame{Kind: pluginv1.FrameKind_EVENT, RequestId: frame.GetRequestId()})
	}()
	err := NewClient(left, 0).Call(context.Background(), "health", "", &pluginv1.Health{}, &pluginv1.Health{})
	if err != ErrProtocolViolation {
		t.Fatalf("expected protocol violation, got %v", err)
	}
}
