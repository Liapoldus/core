package main

import (
	"bufio"
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
	defer func() { _ = runtime.Stop() }()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	updates := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			updates <- scanner.Text()
		}
	}()

	for {
		select {
		case <-shutdown:
			return
		case path := <-updates:
			candidate, readErr := os.ReadFile(path)
			if readErr != nil {
				fmt.Fprintln(os.Stdout, "rejected")
				continue
			}
			if _, updateErr := runtime.ReplaceCaddyfile(candidate); updateErr != nil {
				fmt.Fprintln(os.Stdout, "rejected")
				continue
			}
			fmt.Fprintln(os.Stdout, "replaced")
		}
	}
}
