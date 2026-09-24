package main

import (
	"encoding/json"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/caddy"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(caddy.RegisteredModules()); err != nil {
		os.Exit(1)
	}
}
