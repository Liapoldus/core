package main

import (
	"os"

	"github.com/Liapoldus/core/internal/presentation/cli"
)

func main() {
	// Product runtimes are configured as ordinary plugin instances in Core.
	// The composition root must not construct or link a product-specific runtime.
	os.Exit(cli.Execute(os.Args[1:]))
}
