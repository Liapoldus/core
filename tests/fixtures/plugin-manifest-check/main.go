package main

import (
	"encoding/json"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/encoding/protojson"
)

type input struct {
	Manifest  json.RawMessage `json:"manifest"`
	Required  []string        `json:"required"`
}

func main() {
	var request input
	if err := json.Unmarshal([]byte(os.Args[1]), &request); err != nil {
		os.Exit(1)
	}
	var manifest pluginv1.Manifest
	if err := protojson.Unmarshal(request.Manifest, &manifest); err != nil {
		os.Exit(1)
	}
	err := plugins.ValidateManifest(&manifest, request.Required)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]bool{"valid": err == nil}); err != nil {
		os.Exit(1)
	}
}
