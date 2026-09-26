package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/caddy"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	address, endpoint := os.Args[1], os.Args[2]
	source := []byte("http://" + address + " {\n  route {\n    liapoldus_plugin fixture forms.http http-stream\n  }\n}\n")
	instance := caddy.PluginInstance{
		Name: "fixture", Endpoint: endpoint, Timeout: 3 * time.Second,
		StartTimeout: 3 * time.Second, MaxConcurrentCalls: 8,
		CookiePolicies: []json.RawMessage{json.RawMessage(`{"version":1,"instanceId":"fixture","capability":"forms.http","allowedNames":["session"]}`)},
	}
	runtime, _, err := caddy.StartCaddyfileWithPlugins(source, []caddy.PluginInstance{instance})
	if err != nil {
		panic(err)
	}
	defer runtime.Stop()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]bool{"ready": true}); err != nil {
		panic(err)
	}
}
