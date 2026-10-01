package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

type client struct {
	calls *int
	err   error
}

func (c client) Reload(_ context.Context, _ sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	*c.calls++
	return sdkmodels.ReloadAcknowledgement{}, c.err
}

func main() {
	counts := []int{0, 0, 0}
	fanout := &plugins.SDKReloadFanout{InstanceID: "fixture", Replicas: []plugins.SDKReloadReplicaClient{
		{ReplicaID: "first", Client: client{calls: &counts[0]}},
		{ReplicaID: "second", Client: client{calls: &counts[1], err: errors.New("refused")}},
		{ReplicaID: "third", Client: client{calls: &counts[2]}},
	}}
	_, results, err := fanout.ReloadObserved(context.Background(), sdkmodels.Reload{})
	var nilFanout *plugins.SDKReloadFanout
	_, _, nilErr := nilFanout.ReloadObserved(context.Background(), sdkmodels.Reload{})
	replicas := make([]map[string]any, 0, len(results))
	for _, result := range results {
		replicas = append(replicas, map[string]any{
			"id": result.ReplicaID, "acknowledged": result.Acknowledged,
			"unreachable": result.Unreachable,
		})
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"error": err != nil, "calls": counts, "replicas": replicas,
		"nilFanoutUnavailable": errors.Is(nilErr, plugins.ErrPluginUnavailable),
	}); encodeErr != nil {
		panic(encodeErr)
	}
}
