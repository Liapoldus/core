package plugins

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/Liapoldus/pluginprotocol/framing"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/proto"
)

func TestCapabilityClientDispatchesHTTPAndL4ThroughProtocol(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			frame, err := framing.Decode(right)
			if err != nil {
				done <- err
				return
			}
			envelope := new(pluginv1.Envelope)
			if err := proto.Unmarshal(frame.GetPayload(), envelope); err != nil {
				done <- err
				return
			}
			var response []byte
			switch envelope.GetCapability() {
			case "http.proxy":
				response, _ = json.Marshal(HTTPResponse{Status: 204, Headers: map[string]string{"x-plugin": "ok"}})
			case "tcp.filter":
				response, _ = json.Marshal(L4Response{Payload: []byte("filtered")})
			default:
				done <- ErrProtocolViolation
				return
			}
			body, _ := proto.Marshal(&pluginv1.Envelope{Payload: response})
			if err := framing.Encode(right, &pluginv1.Frame{Kind: pluginv1.FrameKind_CALL_RESULT, RequestId: frame.GetRequestId(), Payload: body}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	client, err := NewCapabilityClient(NewClient(left, 0))
	if err != nil {
		t.Fatal(err)
	}
	httpResponse, err := client.HTTP(context.Background(), "http.proxy", HTTPRequest{Method: "GET", Path: "/health", RequestID: "r1"})
	if err != nil || httpResponse.Status != 204 {
		t.Fatalf("http dispatch: response=%+v err=%v", httpResponse, err)
	}
	l4Response, err := client.L4(context.Background(), "tcp.filter", L4Request{Transport: "tcp", Direction: "request", Connection: "c1", Payload: []byte("raw")})
	if err != nil || string(l4Response.Payload) != "filtered" {
		t.Fatalf("l4 dispatch: response=%+v err=%v", l4Response, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityClientRejectsSocketLikeCapabilitiesAndInvalidContext(t *testing.T) {
	client, err := NewCapabilityClient(NewClient(nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.HTTP(context.Background(), "http.proxy", HTTPRequest{}); err == nil {
		t.Fatal("expected invalid HTTP context")
	}
	if _, err := client.L4(context.Background(), "socket.open", L4Request{Transport: "tcp", Direction: "request", Connection: "c"}); err == nil {
		t.Fatal("expected capability allow-list rejection")
	}
}
