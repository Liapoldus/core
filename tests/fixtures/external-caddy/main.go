package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

const buildID = "external-caddy-fixture-build"

type caddyConfig struct {
	Admin struct {
		Listen string `json:"listen"`
	} `json:"admin"`
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				Listen []string `json:"listen"`
			} `json:"servers"`
		} `json:"http"`
	} `json:"apps"`
	FixtureGeneration string `json:"fixtureGeneration"`
}

type event struct {
	Name        string `json:"name"`
	PID         int    `json:"pid"`
	AdminListen string `json:"adminListen,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "seed" {
		if err := seed(os.Args[2:]); err != nil {
			fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "component-host" {
		if err := runComponentHost(os.Args[2:]); err != nil {
			fatal(err)
		}
		return
	}
	for _, argument := range os.Args[1:] {
		if strings.Contains(argument, "version") {
			fmt.Println(buildID)
			return
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "adapt" {
		if err := adapt(os.Args[2:]); err != nil {
			fatal(err)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fatal(err)
	}
}

func adapt(arguments []string) error {
	configPath := ""
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--config" {
			configPath = arguments[index+1]
			break
		}
	}
	if configPath == "" {
		return fmt.Errorf("Caddy adapt fixture requires the standard config argument")
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	address := regexp.MustCompile(`http://([^\s{]+)`).FindSubmatch(contents)
	if len(address) != 2 {
		return fmt.Errorf("fixture Caddyfile has no HTTP listener")
	}
	configuration := map[string]any{
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"fixture": map[string]any{"listen": []string{string(address[1])}},
				},
			},
		},
		"fixtureGeneration": strings.TrimSpace(string(contents)),
	}
	return json.NewEncoder(os.Stdout).Encode(configuration)
}

type hostCommand struct {
	Action string `json:"action"`
	Path   string `json:"path"`
}

func runComponentHost(arguments []string) error {
	if len(arguments) != 4 {
		return fmt.Errorf("component host expects external binary, state directory, Caddyfile, and build identity")
	}
	initial, err := os.ReadFile(arguments[2])
	if err != nil {
		return err
	}
	runtime, err := caddy.StartExternal(context.Background(), caddy.ExternalOptions{
		Binary: arguments[0], StateDirectory: arguments[1], ExpectedBuildID: arguments[3],
	}, initial)
	if err != nil {
		return err
	}
	defer runtime.Stop()
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{"event": "ready", "adminSocket": runtime.AdminSocketPath()}); err != nil {
		return err
	}
	input := make(chan hostCommand)
	inputErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var command hostCommand
			if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
				inputErrors <- err
				return
			}
			input <- command
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
		case command := <-input:
			switch command.Action {
			case "ready":
				err := runtime.Ready(context.Background())
				_ = encoder.Encode(map[string]any{"action": command.Action, "ready": err == nil})
			case "activate":
				candidate, readErr := os.ReadFile(command.Path)
				if readErr != nil {
					_ = encoder.Encode(map[string]any{"action": command.Action, "activated": false})
					continue
				}
				activateErr := runtime.Activate(context.Background(), candidate)
				_ = encoder.Encode(map[string]any{"action": command.Action, "activated": activateErr == nil})
			case "stop":
				if err := runtime.Stop(); err != nil {
					return err
				}
				_ = encoder.Encode(map[string]any{"event": "stopped"})
				return nil
			default:
				_ = encoder.Encode(map[string]any{"action": command.Action, "error": true})
			}
		}
	}
}

func seed(arguments []string) error {
	if len(arguments) != 3 {
		return fmt.Errorf("fixture seed expects database, artifact root, and public address")
	}
	databasePath, artifactRoot, publicAddress := arguments[0], arguments[1], arguments[2]
	caddyfile := []byte("http://" + publicAddress + " {\n  respond \"external-active\"\n}\n")
	const revisionID = "external-fixture-initial-revision"
	revisionDirectory := filepath.Join(artifactRoot, "releases", revisionID)
	if err := os.MkdirAll(revisionDirectory, 0o700); err != nil {
		return err
	}
	relativePath := filepath.Join("releases", revisionID, "Caddyfile")
	if err := os.WriteFile(filepath.Join(artifactRoot, relativePath), caddyfile, 0o600); err != nil {
		return err
	}
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return err
	}
	database, err := storage.OpenSQLite(context.Background(), databasePath, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	if err != nil {
		return err
	}
	defer database.Close()
	groups, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(caddyfile)
	if _, err := groups.CreateRevision(context.Background(), models.GroupRevision{
		ID: revisionID, GroupID: "system", CaddyfileDigest: hex.EncodeToString(digest[:]),
		CaddyfilePath: relativePath, Actor: "external-caddy-fixture",
	}); err != nil {
		return err
	}
	_, err = groups.AdvanceCurrent(context.Background(), "system", revisionID, nil)
	return err
}

func run(arguments []string) error {
	configPath := ""
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--config" {
			configPath = arguments[index+1]
			break
		}
	}
	if configPath == "" {
		return fmt.Errorf("external Caddy fixture requires the standard --config argument")
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var configuration caddyConfig
	if err := json.Unmarshal(contents, &configuration); err != nil {
		return err
	}
	_, socketPath, ok := unixAddress(configuration.Admin.Listen)
	if !ok {
		return fmt.Errorf("external Caddy Admin API must use a Unix socket")
	}
	eventsPath := os.Getenv("LIAPOLDUS_TEST_EXTERNAL_CADDY_EVENTS")
	holdPath := os.Getenv("LIAPOLDUS_TEST_EXTERNAL_CADDY_HOLD")
	if eventsPath == "" || holdPath == "" {
		return fmt.Errorf("external Caddy fixture control paths are missing")
	}
	if err := appendEvent(eventsPath, event{Name: "started", PID: os.Getpid(), AdminListen: configuration.Admin.Listen}); err != nil {
		return err
	}
	for _, server := range configuration.Apps.HTTP.Servers {
		for _, address := range server.Listen {
			if err := servePublic(address, holdPath); err != nil {
				_ = appendEvent(eventsPath, event{Name: "public-listener-failed", PID: os.Getpid(), AdminListen: configuration.Admin.Listen, Detail: "listener unavailable"})
			}
		}
	}
	for fileExists(holdPath) {
		if err := appendEvent(eventsPath, event{Name: "waiting", PID: os.Getpid(), AdminListen: configuration.Admin.Listen}); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return err
	}
	admin := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/config/" {
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(configuration)
			return
		}
		if request.URL.Path == "/load" && request.Method == http.MethodPost {
			rejectPath := os.Getenv("LIAPOLDUS_TEST_EXTERNAL_CADDY_REJECT")
			if fileExists(rejectPath) {
				_ = os.Remove(rejectPath)
				http.Error(response, "rejected", http.StatusInternalServerError)
				return
			}
			var candidate caddyConfig
			if err := json.NewDecoder(request.Body).Decode(&candidate); err != nil {
				http.Error(response, "invalid", http.StatusBadRequest)
				return
			}
			configuration = candidate
			response.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(response, request)
	})}
	adminDone := make(chan error, 1)
	go func() { adminDone <- admin.Serve(listener) }()
	if err := appendEvent(eventsPath, event{Name: "ready", PID: os.Getpid(), AdminListen: configuration.Admin.Listen}); err != nil {
		return err
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	_ = appendEvent(eventsPath, event{Name: "stopping", PID: os.Getpid(), AdminListen: configuration.Admin.Listen})
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = admin.Shutdown(shutdown)
	_ = os.Remove(socketPath)
	return nil
}

func servePublic(address, holdPath string) error {
	address = strings.TrimPrefix(address, "tcp/")
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/" && !fileExists(holdPath) {
			_, _ = response.Write([]byte("external-active"))
			return
		}
		http.NotFound(response, request)
	})}
	go func() { _ = server.Serve(listener) }()
	return nil
}

func unixAddress(value string) (string, string, bool) {
	const prefix = "unix//"
	if !strings.HasPrefix(value, prefix) {
		return "", "", false
	}
	path := strings.TrimPrefix(value, prefix)
	if path == "" || !filepath.IsAbs(path) {
		return "", "", false
	}
	return value, path, true
}

func appendEvent(path string, value event) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(value)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
