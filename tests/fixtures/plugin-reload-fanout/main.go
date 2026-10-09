package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

type client struct {
	stats       *callStats
	index       int
	err         error
	mismatch    bool
	current     bool
	wrongSchema bool
}

type callStats struct {
	mu                sync.Mutex
	calls             [4]int
	active            int
	maxConcurrentCall int
}

func (c client) Readiness(context.Context) (sdkmodels.Readiness, error) {
	if !c.current && !c.wrongSchema {
		return sdkmodels.Readiness{}, errors.New("not ready")
	}
	schemaVersion := "1"
	if c.wrongSchema {
		schemaVersion = "2"
	}
	return sdkmodels.Readiness{
		Ready: true, Generation: "7", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SchemaVersion: schemaVersion, InstanceID: "fixture", ReplicaID: []string{"first", "second", "third", "fourth"}[c.index],
	}, nil
}

func (c client) Reload(_ context.Context, request sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	c.stats.mu.Lock()
	c.stats.calls[c.index]++
	c.stats.active++
	if c.stats.active > c.stats.maxConcurrentCall {
		c.stats.maxConcurrentCall = c.stats.active
	}
	c.stats.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	c.stats.mu.Lock()
	c.stats.active--
	c.stats.mu.Unlock()
	ack := sdkmodels.ReloadAcknowledgement{
		Generation: request.Generation, SHA256: request.SHA256, SchemaVersion: request.SchemaVersion,
		Applied: true, Outcome: sdkmodels.OutcomeApplied,
	}
	if c.mismatch {
		ack.Generation = "wrong-generation"
	}
	return ack, c.err
}

func main() {
	stats := &callStats{}
	request := sdkmodels.Reload{Generation: "7", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SchemaVersion: "1"}
	fanout := &plugins.SDKReloadFanout{InstanceID: "fixture", Replicas: []plugins.SDKReloadReplicaClient{
		{ReplicaID: "first", Client: client{stats: stats, index: 0, current: true}},
		{ReplicaID: "second", Client: client{stats: stats, index: 1, err: errors.New("refused")}},
		{ReplicaID: "third", Client: client{stats: stats, index: 2, wrongSchema: true}},
		{ReplicaID: "fourth", Client: client{stats: stats, index: 3, mismatch: true}},
	}}
	_, results, err := fanout.ReloadObserved(context.Background(), request)
	stats.mu.Lock()
	counts := stats.calls
	maxConcurrentCalls := stats.maxConcurrentCall
	stats.mu.Unlock()
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
		"error": err != nil, "protocolViolation": errors.Is(err, plugins.ErrProtocolViolation),
		"calls": counts, "maxConcurrentCalls": maxConcurrentCalls, "replicas": replicas,
		"nilFanoutUnavailable": errors.Is(nilErr, plugins.ErrPluginUnavailable),
	}); encodeErr != nil {
		panic(encodeErr)
	}
}
