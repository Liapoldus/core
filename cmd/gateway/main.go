package main

import (
	"os"

	_ "github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/presentation/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
