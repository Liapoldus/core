package network

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

type testHTTPCapability struct{ request plugins.HTTPRequest }

func (c *testHTTPCapability) HTTP(_ context.Context, _ string, request plugins.HTTPRequest) (plugins.HTTPResponse, error) {
	c.request = request
	return plugins.HTTPResponse{Status: 201, Headers: map[string]string{"X-Plugin": "ok"}, Body: []byte("created")}, nil
}

func TestServeWithCapabilitiesKeepsHTTPBoundary(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	_ = probe.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capability := &testHTTPCapability{}
	route := models.Route{When: models.PathMatcher{Prefixes: []string{"/submit"}}, Plugin: &models.PluginTarget{Instance: "forms", Capability: "forms.submit"}}
	done := make(chan error, 1)
	go func() {
		done <- ServeWithCapabilities(ctx, []models.Listener{{Type: "http", IsHTTP: true, Address: address, Routes: []models.Route{route}}}, nil, nil, nil, 0, map[string]HTTPCapabilityDispatcher{"forms": capability})
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	var response *http.Response
	for i := 0; i < 40; i++ {
		response, err = client.Post("http://"+address+"/submit", "text/plain", &readerBody{value: "payload"})
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 201 || string(body) != "created" {
		t.Fatalf("response=%d body=%s", response.StatusCode, body)
	}
	if capability.request.Headers["Authorization"] != "" || capability.request.Headers["Cookie"] != "" {
		t.Fatal("credential headers crossed plugin boundary")
	}
	if string(capability.request.Body) != "payload" {
		t.Fatalf("body=%q", capability.request.Body)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

type readerBody struct{ value string }

func (r *readerBody) Read(p []byte) (int, error) {
	if r.value == "" {
		return 0, io.EOF
	}
	n := copy(p, r.value)
	r.value = r.value[n:]
	return n, nil
}
func (*readerBody) Close() error { return nil }
