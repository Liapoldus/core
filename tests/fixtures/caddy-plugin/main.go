package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/caddy"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	contents, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runtime, _, err := caddy.StartCaddyfileWithPlugins(contents, []caddy.PluginInstance{{
		Name:               "fixture",
		Endpoint:           os.Args[2],
		Timeout:            3 * time.Second,
		StartTimeout:       3 * time.Second,
		MaxConcurrentCalls: 8,
	}})
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
