package main

import (
	"encoding/json"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	loaded, err := config.LoadTrafficController(os.Args[1])
	if err != nil {
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(loaded); err != nil {
		os.Exit(1)
	}
}
