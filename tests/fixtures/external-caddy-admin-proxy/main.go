package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	caddyBinaryEnvironment       = "LIAPOLDUS_TEST_EXTERNAL_CADDY_BINARY"
	eventsPathEnvironment        = "LIAPOLDUS_TEST_EXTERNAL_CADDY_EVENTS"
	activationBarrierEnvironment = "LIAPOLDUS_TEST_EXTERNAL_CADDY_ACTIVATION_BARRIER"
	activationTokenEnvironment   = "LIAPOLDUS_TEST_EXTERNAL_CADDY_ACTIVATION_TOKEN"
	unixPrefix                   = "unix/"
)

type configuration struct {
	Admin struct {
		Listen string `json:"listen"`
	} `json:"admin"`
}

type event struct {
	Name     string `json:"name"`
	PID      int    `json:"pid"`
	ChildPID int    `json:"childPid,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("missing Caddy command")
	}
	caddyBinary := os.Getenv(caddyBinaryEnvironment)
	if caddyBinary == "" {
		return errors.New("missing custom Caddy binary")
	}
	if arguments[0] != "run" {
		command := exec.Command(caddyBinary, arguments...)
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		return command.Run()
	}
	return runWithAdminProxy(caddyBinary, arguments[1:])
}

func runWithAdminProxy(caddyBinary string, arguments []string) error {
	configurationPath := argumentValue(arguments, "--config")
	if configurationPath == "" {
		return errors.New("missing Caddy config path")
	}
	contents, err := os.ReadFile(configurationPath)
	if err != nil {
		return err
	}
	var initial map[string]any
	if err := json.Unmarshal(contents, &initial); err != nil {
		return err
	}
	var parsed configuration
	if err := json.Unmarshal(contents, &parsed); err != nil {
		return err
	}
	if !strings.HasPrefix(parsed.Admin.Listen, unixPrefix) {
		return errors.New("Caddy Admin API must use a Unix socket")
	}
	publicSocketPath := strings.TrimPrefix(parsed.Admin.Listen, unixPrefix)
	upstreamSocketPath := publicSocketPath + ".upstream"
	upstreamAdminAddress := unixPrefix + upstreamSocketPath
	if err := rewriteAdminListen(initial, upstreamAdminAddress); err != nil {
		return err
	}
	upstreamConfigPath := configurationPath + ".upstream"
	if err := writeJSONFile(upstreamConfigPath, initial); err != nil {
		return err
	}
	defer os.Remove(upstreamConfigPath)

	commandArguments := rewriteArgument(arguments, "--config", upstreamConfigPath)
	child := exec.Command(caddyBinary, append([]string{"run"}, commandArguments...)...)
	child.Stdout = os.Stderr
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		return err
	}
	childPID := child.Process.Pid
	if err := appendEvent(event{Name: "started", PID: os.Getpid(), ChildPID: childPID}); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		return err
	}

	_ = os.Remove(publicSocketPath)
	listener, err := net.Listen("unix", publicSocketPath)
	if err != nil {
		_ = child.Process.Signal(syscall.SIGTERM)
		_ = child.Wait()
		return err
	}
	if err := os.Chmod(publicSocketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = child.Process.Signal(syscall.SIGTERM)
		_ = child.Wait()
		return err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", upstreamSocketPath)
	}}
	defer transport.CloseIdleConnections()
	var readyOnce sync.Once
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		proxyRequest(response, request, transport, upstreamAdminAddress, func() {
			readyOnce.Do(func() { _ = appendEvent(event{Name: "ready", PID: os.Getpid(), ChildPID: childPID}) })
		}, childPID)
	})
	adminServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- adminServer.Serve(listener) }()

	childDone := make(chan error, 1)
	go func() { childDone <- child.Wait() }()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	select {
	case err := <-childDone:
		_ = adminServer.Close()
		_ = os.Remove(publicSocketPath)
		return err
	case <-stop:
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = adminServer.Shutdown(shutdownContext)
		_ = child.Process.Signal(syscall.SIGTERM)
		<-childDone
		_ = os.Remove(publicSocketPath)
		return nil
	case err := <-serverDone:
		_ = child.Process.Signal(syscall.SIGTERM)
		<-childDone
		_ = os.Remove(publicSocketPath)
		return err
	}
}

func proxyRequest(
	response http.ResponseWriter,
	request *http.Request,
	transport *http.Transport,
	upstreamAdminAddress string,
	markReady func(),
	childPID int,
) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(response, "invalid admin request", http.StatusBadRequest)
		return
	}
	barrierCandidate := false
	if request.URL.Path == "/load" && request.Method == http.MethodPost {
		var candidate map[string]any
		if err := json.Unmarshal(body, &candidate); err != nil {
			http.Error(response, "invalid Caddy config", http.StatusBadRequest)
			return
		}
		if err := rewriteAdminListen(candidate, upstreamAdminAddress); err != nil {
			http.Error(response, "invalid Caddy admin config", http.StatusBadRequest)
			return
		}
		body, err = json.Marshal(candidate)
		if err != nil {
			http.Error(response, "invalid Caddy config", http.StatusBadRequest)
			return
		}
		token := os.Getenv(activationTokenEnvironment)
		barrierCandidate = token != "" && strings.Contains(string(body), token)
	}

	proxyRequest := request.Clone(request.Context())
	proxyRequest.URL = &url.URL{Scheme: "http", Host: "caddy", Path: request.URL.Path, RawQuery: request.URL.RawQuery}
	proxyRequest.RequestURI = ""
	proxyRequest.Body = io.NopCloser(bytes.NewReader(body))
	proxyRequest.ContentLength = int64(len(body))
	proxyRequest.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	upstreamResponse, err := transport.RoundTrip(proxyRequest)
	if err != nil {
		http.Error(response, "Caddy Admin API unavailable", http.StatusBadGateway)
		return
	}
	defer upstreamResponse.Body.Close()
	for name, values := range upstreamResponse.Header {
		for _, value := range values {
			response.Header().Add(name, value)
		}
	}
	response.WriteHeader(upstreamResponse.StatusCode)
	if barrierCandidate && upstreamResponse.StatusCode >= http.StatusOK && upstreamResponse.StatusCode < http.StatusMultipleChoices {
		if err := appendEvent(event{Name: "activation-blocked", PID: os.Getpid(), ChildPID: childPID, Detail: os.Getenv(activationTokenEnvironment)}); err != nil {
			return
		}
		barrierPath := os.Getenv(activationBarrierEnvironment)
		for fileExists(barrierPath) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if request.URL.Path == "/config/" && upstreamResponse.StatusCode >= http.StatusOK && upstreamResponse.StatusCode < http.StatusMultipleChoices {
		markReady()
	}
	_, _ = io.Copy(response, upstreamResponse.Body)
}

func rewriteAdminListen(value map[string]any, address string) error {
	admin, ok := value["admin"].(map[string]any)
	if !ok {
		return errors.New("Caddy config has no Admin API")
	}
	admin["listen"] = address
	return nil
}

func writeJSONFile(path string, value any) error {
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0o600)
}

func appendEvent(value event) error {
	path := os.Getenv(eventsPathEnvironment)
	if path == "" {
		return errors.New("missing fixture events path")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(value)
}

func argumentValue(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func rewriteArgument(arguments []string, name, replacement string) []string {
	result := append([]string(nil), arguments...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == name {
			result[index+1] = replacement
		}
	}
	return result
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
