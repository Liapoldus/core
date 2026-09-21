package main

import (
	"os"

	"github.com/Liapoldus/core/internal/presentation/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
