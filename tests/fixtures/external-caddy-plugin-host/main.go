package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/caddy"
)

type command struct {
	Action string `json:"action"`
	Path   string `json:"path"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) != 5 {
		return fmt.Errorf("external Caddy host requires binary, state directory, initial file, build identity, and plugin endpoint")
	}
	initial, err := os.ReadFile(arguments[2])
	if err != nil {
		return err
	}
	runtime, err := caddy.StartExternal(context.Background(), caddy.ExternalOptions{
		Binary: arguments[0], ExpectedBuildID: arguments[3], StateDirectory: arguments[1],
		PluginInstances: []caddy.PluginInstance{{
			Name: "fixture", Endpoint: arguments[4], Timeout: 3 * time.Second,
			StartTimeout: 3 * time.Second, MaxConcurrentCalls: 8,
		}},
	}, initial)
	if err != nil {
		return err
	}
	defer runtime.Stop()
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{"event": "ready", "ready": runtime.Ready(context.Background()) == nil}); err != nil {
		return err
	}
	commands := make(chan command)
	inputErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var next command
			if err := json.Unmarshal(scanner.Bytes(), &next); err != nil {
				inputErrors <- err
				return
			}
			commands <- next
		}
		inputErrors <- scanner.Err()
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	for {
		select {
		case <-stop:
			return nil
		case err := <-inputErrors:
			return err
		case next := <-commands:
			switch next.Action {
			case "activate":
				candidate, readErr := os.ReadFile(next.Path)
				active := readErr == nil && runtime.Activate(context.Background(), candidate) == nil
				if err := encoder.Encode(map[string]any{"action": next.Action, "activated": active}); err != nil {
					return err
				}
			case "stop":
				return nil
			default:
				if err := encoder.Encode(map[string]any{"action": next.Action, "error": true}); err != nil {
					return err
				}
			}
		}
	}
}
