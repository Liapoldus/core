package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Liapoldus/core/internal/infrastructure/caddy"
)

func main() {
	contents, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runtime, _, err := caddy.StartCaddyfile(contents)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	<-shutdown
	if err := runtime.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
