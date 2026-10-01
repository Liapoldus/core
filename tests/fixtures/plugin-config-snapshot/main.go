package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
	sdk "github.com/Liapoldus/plugin-sdk/infrastructure"
)

type durable struct {
	current  models.PluginConfigurationRevision
	previous models.PluginConfigurationRevision
	reads    int
	blocked  bool
}

func (source *durable) Current(context.Context, string) (models.PluginConfigurationRevision, models.PluginConfigurationPointers, error) {
	source.reads++
	if source.blocked {
		panic("durable read during request")
	}
	return source.current, models.PluginConfigurationPointers{
		CurrentRevision: source.current.Revision, PreviousRevision: source.previous.Revision,
	}, nil
}

func (source *durable) GetRevision(_ context.Context, _ string, revision int64) (models.PluginConfigurationRevision, error) {
	source.reads++
	if source.blocked {
		panic("durable read during request")
	}
	if source.previous.Revision == revision {
		return source.previous, nil
	}
	return models.PluginConfigurationRevision{}, models.PluginConfigurationNotFound{}
}

func revision(number int64, raw string) models.PluginConfigurationRevision {
	digest := sha256.Sum256([]byte(raw))
	return models.PluginConfigurationRevision{InstanceID: "fixture", Revision: number, SchemaVersion: 1,
		Digest: hex.EncodeToString(digest[:]), SettingsJSON: []byte(raw)}
}

func main() {
	first := revision(1, `{ "value": 1 }`)
	source := &durable{current: first}
	snapshot, err := storage.NewPluginConfigurationSnapshot(context.Background(), source, []string{"fixture"})
	check(err)
	contract, err := sdk.LoadHTTPContract()
	check(err)
	endpoint := contract.Core.ConfigPull
	handler, err := api.NewPluginConfigurationPullHandler(snapshot, func(*x509.Certificate) (string, bool) { return "fixture", true })
	check(err)
	pull := func(number string) map[string]any {
		path := strings.Replace(endpoint.PathTemplate, "{generation}", number, 1)
		request := httptest.NewRequest(endpoint.Method, path, nil)
		certificate := &x509.Certificate{}
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		body := response.Body.String()
		digest := sha256.Sum256([]byte(body))
		return map[string]any{"status": response.Code, "body": body,
			"digestValid": response.Header().Get(endpoint.ResponseHeaders["sha256"]) == hex.EncodeToString(digest[:])}
	}
	source.blocked = true
	initial := pull("1")
	source.blocked = false
	source.previous, source.current = first, revision(2, `{"value":2}`)
	check(snapshot.Refresh(context.Background(), "fixture"))
	source.blocked = true
	reads := source.reads
	promoted, previous := pull("2"), pull("1")
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{"initial": initial, "promoted": promoted,
		"previous": previous, "durableReadsDuringPull": source.reads - reads}))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
