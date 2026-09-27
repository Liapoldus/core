package caddy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
)

type AdminClientOptions struct {
	SnapshotPath           string
	PathPrefix             string
	RequestBodyBytes       int64
	SnapshotBytes          int64
	ResponseBodyBytes      int64
	RequestTimeout         time.Duration
	ForwardRequestHeaders  []string
	ForwardResponseHeaders []string
	InvalidConfiguration   string
	SnapshotUnavailable    string
	AdminUnavailable       string
}

type AdminRuntimeOptions struct {
	Client                AdminClientOptions
	UnixPrefix            string
	UnixNetwork           string
	URLScheme             string
	URLHost               string
	SocketDirectoryPrefix string
	SocketName            string
	DirectoryMode         uint32
	SocketMode            uint32
}

type AdminClient struct {
	baseURL string
	client  *http.Client
	options AdminClientOptions
}

func NewAdminClient(baseURL string, client *http.Client, options AdminClientOptions) (*AdminClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || client == nil || options.SnapshotPath == "" || options.PathPrefix == "" || options.RequestBodyBytes < 1 || options.SnapshotBytes < 1 || options.ResponseBodyBytes < 1 || options.RequestTimeout <= 0 || options.InvalidConfiguration == "" || options.AdminUnavailable == "" {
		return nil, errors.New(options.InvalidConfiguration)
	}
	return &AdminClient{baseURL: strings.TrimRight(baseURL, options.PathPrefix), client: client, options: options}, nil
}

func NewUnixAdminClient(socketPath string, options AdminRuntimeOptions) (*AdminClient, *http.Client, error) {
	if socketPath == "" || options.UnixNetwork == "" || options.URLScheme == "" || options.URLHost == "" || options.Client.RequestTimeout <= 0 {
		return nil, nil, errors.New(options.Client.InvalidConfiguration)
	}
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, options.UnixNetwork, socketPath)
		},
	}
	client := &http.Client{Transport: transport, Timeout: options.Client.RequestTimeout}
	endpoint := (&url.URL{Scheme: options.URLScheme, Host: options.URLHost}).String()
	adminClient, err := NewAdminClient(endpoint, client, options.Client)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, err
	}
	return adminClient, client, nil
}

func (client *AdminClient) Snapshot(ctx context.Context) ([]byte, error) {
	response, err := client.Request(ctx, models.CaddyAdminRequest{Method: http.MethodGet, Path: client.options.SnapshotPath})
	if err != nil {
		return nil, err
	}
	if response.Status < http.StatusOK || response.Status >= http.StatusMultipleChoices {
		return nil, errors.New(client.options.SnapshotUnavailable)
	}
	if int64(len(response.Body)) > client.options.SnapshotBytes {
		return nil, errors.New(client.options.SnapshotUnavailable)
	}
	return response.Body, nil
}

func (client *AdminClient) Request(ctx context.Context, value models.CaddyAdminRequest) (models.CaddyAdminResponse, error) {
	if client == nil || value.Method == "" || value.Path == "" || int64(len(value.Body)) > client.options.RequestBodyBytes {
		return models.CaddyAdminResponse{}, errors.New(client.invalidConfiguration())
	}
	parsedPath, err := url.ParseRequestURI(value.Path)
	if err != nil || parsedPath.IsAbs() || parsedPath.Host != "" || !strings.HasPrefix(parsedPath.Path, client.options.PathPrefix) {
		return models.CaddyAdminResponse{}, errors.New(client.options.InvalidConfiguration)
	}
	request, err := http.NewRequestWithContext(ctx, value.Method, client.baseURL+value.Path, bytes.NewReader(value.Body))
	if err != nil {
		return models.CaddyAdminResponse{}, errors.New(client.options.InvalidConfiguration)
	}
	for _, name := range client.options.ForwardRequestHeaders {
		for _, headerValue := range value.Headers[textproto.CanonicalMIMEHeaderKey(name)] {
			request.Header.Add(name, headerValue)
		}
	}
	response, err := client.client.Do(request)
	if err != nil {
		return models.CaddyAdminResponse{}, errors.New(client.options.AdminUnavailable)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, client.options.ResponseBodyBytes+1))
	if err != nil || int64(len(body)) > client.options.ResponseBodyBytes {
		return models.CaddyAdminResponse{}, errors.New(client.options.AdminUnavailable)
	}
	headers := make(map[string][]string)
	for _, name := range client.options.ForwardResponseHeaders {
		if values := response.Header.Values(name); len(values) > 0 {
			headers[name] = append([]string(nil), values...)
		}
	}
	return models.CaddyAdminResponse{Status: response.StatusCode, Headers: headers, Body: body}, nil
}

func (client *AdminClient) invalidConfiguration() string {
	if client == nil {
		return ""
	}
	return client.options.InvalidConfiguration
}
