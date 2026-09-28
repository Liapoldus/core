package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

func main() {
	if len(os.Args) != 2 {
		panic("plugin binary is required")
	}
	instance := models.PluginInstance{
		Binary: os.Args[1], Capabilities: []string{"test.lifecycle"},
		Settings: []byte(`{"mode":"initial"}`), SettingsRevision: "1",
		Timeout: time.Second, StartTimeout: 5 * time.Second, MaxConcurrentCalls: 1,
		HealthProbeInterval: time.Hour, HealthFailureThreshold: 1,
		MemoryProbeInterval: time.Hour, MemoryLimitBytes: ^uint64(0),
	}
	runtime, err := plugins.StartRuntime(context.Background(), map[string]models.PluginInstance{"fixture": instance}, nil)
	check(err)
	defer runtime.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rejected := runtime.ApplyConfiguration(ctx, "fixture", "2", []byte(`{"reject":true}`))
	mismatched := runtime.ApplyConfiguration(ctx, "fixture", "2", []byte(`{"wrongRevision":true}`))
	accepted := runtime.ApplyConfiguration(ctx, "fixture", "2", []byte(`{"mode":"updated"}`))
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"rejectedApplyStayedInactive": rejected != nil,
		"mismatchedAckRejected":       mismatched != nil,
		"exactRevisionAcknowledged":   accepted == nil,
	}))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
