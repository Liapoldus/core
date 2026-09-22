// Package cli adapts operator commands to Gateway application services.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	accountstore "github.com/Liapoldus/core/internal/infrastructure/accounts"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/network"
	"github.com/Liapoldus/core/internal/infrastructure/observability"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/presentation/api"
)

var words = func() config.CLIWords {
	loaded, err := config.LoadCLI()
	if err != nil {
		panic(err)
	}
	return loaded
}()

type options struct {
	output       string
	config       string
	configDir    string
	noManagement bool
	command      []string
}

func Execute(arguments []string) int {
	options, err := parseOptions(arguments)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, err.Error())
		return words.Exits.Arguments
	}
	return run(options)
}

func parseOptions(arguments []string) (options, error) {
	result := options{output: words.Outputs.Text}
	for len(arguments) > 0 {
		switch arguments[0] {
		case words.Flags.Output:
			if len(arguments) < 2 || (arguments[1] != words.Outputs.Text && arguments[1] != words.Outputs.JSON) {
				return options{}, errors.New(words.Diagnostics.OutputInvalid)
			}
			result.output = arguments[1]
			arguments = arguments[2:]
		case words.Flags.Config:
			if len(arguments) < 2 {
				return options{}, errors.New(words.Diagnostics.ConfigRequired)
			}
			result.config = arguments[1]
			arguments = arguments[2:]
		case words.Flags.ConfigDir:
			if len(arguments) < 2 {
				return options{}, errors.New(words.Diagnostics.ConfigDirRequired)
			}
			result.configDir = arguments[1]
			arguments = arguments[2:]
		case words.Flags.NoManagement:
			result.noManagement = true
			arguments = arguments[1:]
		default:
			result.command = arguments
			return result, nil
		}
	}
	return result, nil
}

func run(options options) int {
	if len(options.command) == 0 {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
	if options.command[0] == words.Commands.Serve {
		return serve(options)
	}
	if options.command[0] == words.Commands.Accounts {
		return accounts(options)
	}
	if len(options.command) < 2 || options.command[0] != words.Commands.Config {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}

	switch options.command[1] {
	case words.Subcommands.Path:
		path, source, err := discoverConfig(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		writeSuccess(options.output, map[string]any{
			words.JSON.OK: true, words.JSON.Command: words.Display.Path, words.JSON.Path: path, words.JSON.Source: source,
		})
		return words.Exits.OK
	case words.Subcommands.Validate:
		path, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		if err := config.Validate(path); err != nil {
			return configValidationFailure(options.output, err)
		}
		writeSuccess(options.output, map[string]any{
			words.JSON.OK: true, words.JSON.Command: words.Display.Validate, words.JSON.Valid: true,
		})
		return words.Exits.OK
	case words.Subcommands.Print:
		path, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		if options.output == words.Outputs.JSON {
			document, err := config.PrintJSON(path)
			if err != nil {
				return configValidationFailure(options.output, err)
			}
			writeSuccess(options.output, map[string]any{
				words.JSON.OK: true, words.JSON.Command: words.Display.Print, words.JSON.Document: document,
			})
			return words.Exits.OK
		}
		document, err := config.PrintDocument(path)
		if err != nil {
			return configValidationFailure(options.output, err)
		}
		fmt.Println(strings.TrimRight(string(document), "\n"))
		return words.Exits.OK
	case words.Subcommands.Format:
		path, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		if err := config.FormatFile(path); err != nil {
			return configValidationFailure(options.output, err)
		}
		writeSuccess(options.output, map[string]any{
			words.JSON.OK: true, words.JSON.Command: words.Display.Format, words.JSON.Path: path,
		})
		return words.Exits.OK
	case words.Subcommands.Explain:
		path, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		report, err := config.Explain(path)
		if err != nil {
			return configValidationFailure(options.output, err)
		}
		if options.output == words.Outputs.JSON {
			writeSuccess(options.output, map[string]any{
				words.JSON.OK: true, words.JSON.Command: words.Display.Explain,
				words.JSON.Report: explainJSON(words, report),
			})
			return words.Exits.OK
		}
		for _, listener := range report.Listeners {
			fmt.Println(fmt.Sprintf(words.Explain.Listener, listener.Name, listener.Type, listener.Address))
		}
		for _, route := range report.Routes {
			fmt.Println(fmt.Sprintf(words.Explain.Route, route.Listener, route.Index, route.Site))
		}
		for _, site := range report.Sites {
			fmt.Println(fmt.Sprintf(words.Explain.Site, site.Name, site.Index))
		}
		for _, issue := range report.Issues {
			fmt.Println(fmt.Sprintf(words.Explain.Issue, issue.Kind, issue.Name))
		}
		return words.Exits.OK
	case words.Subcommands.Diff:
		first, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		second, err := diffSecond(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		report, err := config.Diff(first, second)
		if err != nil {
			return configValidationFailure(options.output, err)
		}
		if options.output == words.Outputs.JSON {
			writeSuccess(options.output, map[string]any{
				words.JSON.OK: true, words.JSON.Command: words.Display.Diff,
				words.JSON.Diff: diffJSON(words, report),
			})
			return words.Exits.OK
		}
		for _, change := range report.Added {
			fmt.Println(printDiffChange(words, words.Diff.Added, change))
		}
		for _, change := range report.Removed {
			fmt.Println(printDiffChange(words, words.Diff.Removed, change))
		}
		for _, change := range report.Changed {
			fmt.Println(printDiffChange(words, words.Diff.Changed, change))
		}
		return words.Exits.OK
	default:
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.UnknownConfigCommand)
		return words.Exits.Arguments
	}
}

func accounts(options options) int {
	if len(options.command) < 3 {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.AccountIDRequired)
		return words.Exits.Arguments
	}
	id, action := options.command[2], options.command[1]
	root := options.configDir
	if root == "" {
		root = filepath.Dir(options.config)
		if root == "." || root == "" {
			root = filepath.Dir(words.Paths.DefaultConfig)
		}
	}
	store := accountstore.Store{Root: root, Prefix: words.Accounts.KeyPrefix, Bytes: words.Accounts.KeyBytes, Cost: words.Accounts.HashCost, Extension: words.Accounts.HashExtension}
	var key string
	var err error
	switch action {
	case words.Accounts.Create:
		key, err = store.Create(id)
	case words.Accounts.Rotate:
		key, err = store.Rotate(id)
	case words.Accounts.Revoke:
		err = store.Revoke(id)
	default:
		err = errors.New(words.Diagnostics.CommandExpected)
	}
	if err != nil {
		exit := words.Exits.Internal
		if errors.Is(err, accountstore.ErrConflict) {
			exit = words.Exits.Conflict
		}
		if errors.Is(err, accountstore.ErrNotFound) {
			exit = words.Exits.NotFound
		}
		writeFailure(options.output, exit, words.Codes.ConfigNotFound, err.Error())
		return exit
	}
	if key != "" {
		fmt.Println(key)
	} else {
		fmt.Println(words.Accounts.RevokedMessage)
	}
	return words.Exits.OK
}

func serve(options options) int {
	path, _, err := discoverConfig(options)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
		return words.Exits.Arguments
	}
	graph, err := config.CompileGateway(path)
	if err != nil {
		code := words.Codes.ConfigInvalid
		if config.IsUnknownField(err) {
			code = words.Codes.UnknownField
		}
		writeFailure(options.output, words.Exits.Validation, code, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	drain, err := time.ParseDuration(words.Serve.GracefulTimeout)
	if err != nil {
		writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	metrics := observability.NewRegistry()
	managementWords, wordsErr := config.LoadManagement()
	if wordsErr != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	errorCatalog, wordsErr := config.LoadErrorCatalog()
	if wordsErr != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	providerProblem, exists := errorCatalog.Lookup(words.Codes.WAFProviderUnavailable)
	if !exists {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	bodyTooLargeProblem, exists := errorCatalog.Lookup(words.Codes.BodyTooLarge)
	if !exists {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	dataProviders := security.NewMMDBRegistry(graph.DataProviders)
	defer func() { dataProviders.Close() }()
	wafRuntime := network.NewWAFRuntime(graph, dataProviders.Lookup, providerProblem, bodyTooLargeProblem, managementWords.ContentTypes.Problem)
	management := &api.Server{Token: resolveSecret(graph.Management.StaticToken), ServiceAccounts: graph.Management.ServiceAccounts, Revision: graph.Revision.Value, Digest: graph.Revision.Digest, Metrics: metrics, ValidateConfig: config.ValidateYAML}
	if graph.Management.Listener.TLSProfile != "" {
		profile, ok := graph.TLSProfiles[graph.Management.Listener.TLSProfile]
		if !ok {
			writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
			return words.Exits.Validation
		}
		tlsConfig, tlsErr := network.LoadTLSConfig(profile)
		if tlsErr != nil {
			writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
			return words.Exits.Validation
		}
		management.TLSConfig = tlsConfig
	}
	if raw, readErr := os.ReadFile(path); readErr == nil {
		management.Config = string(raw)
	}
	management.Listeners = make([]any, 0, len(graph.Listeners))
	for _, listener := range graph.Listeners {
		management.Listeners = append(management.Listeners, map[string]any{"address": listener.Address, "http": listener.IsHTTP, "routes": len(listener.Routes)})
	}
	management.Upstreams = make([]any, 0, len(graph.Upstreams))
	for name, upstream := range graph.Upstreams {
		management.Upstreams = append(management.Upstreams, map[string]any{"name": name, "targets": len(upstream.Targets), "balance": upstream.Balance})
	}
	var reloadMu sync.Mutex
	management.ReloadConfig = func(_ context.Context, _ string) (api.Operation, error) {
		reloadMu.Lock()
		defer reloadMu.Unlock()
		reloaded, reloadErr := config.CompileGateway(path)
		if reloadErr != nil {
			return api.Operation{}, reloadErr
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return api.Operation{}, readErr
		}
		nextDataProviders, providerErr := security.NewVerifiedMMDBRegistry(reloaded.DataProviders)
		if providerErr != nil {
			return api.Operation{}, providerErr
		}
		previousDataProviders := dataProviders
		wafRuntime.Replace(reloaded, nextDataProviders.Lookup)
		dataProviders = nextDataProviders
		previousDataProviders.Close()
		management.UpdateRuntimeConfig(reloaded.Revision.Value, reloaded.Revision.Digest, string(contents))
		return api.Operation{ID: "reload-" + reloaded.Revision.Digest[:8], State: "accepted", CreatedAt: time.Now()}, nil
	}
	if !options.noManagement && graph.Management.Listener.Address != "" {
		go func() { _ = management.Listen(ctx, graph.Management.Listener.Address) }()
	}
	if err := network.ServeWithWAFRuntime(ctx, graph.Listeners, graph.Sites, graph.Upstreams, graph.TLSProfiles, graph.RateLimits, wafRuntime, drain, metrics); err != nil {
		writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	return words.Exits.OK
}

func resolveSecret(value string) string {
	if strings.HasPrefix(value, "env:") {
		return os.Getenv(strings.TrimPrefix(value, "env:"))
	}
	if strings.HasPrefix(value, "file:") {
		data, err := os.ReadFile(strings.TrimPrefix(value, "file:"))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	return value
}

func configForValidation(options options) (string, error) {
	if len(options.command) > 2 {
		return absoluteExistingFile(options.command[2])
	}
	path, _, err := discoverConfig(options)
	return path, err
}

func discoverConfig(options options) (string, string, error) {
	if options.config != "" {
		path, err := absoluteExistingFile(options.config)
		return path, words.Sources.Flag, err
	}
	if path := os.Getenv(words.Environment.GatewayConfig); path != "" {
		resolved, err := absoluteExistingFile(path)
		return resolved, words.Sources.Environment, err
	}
	if options.configDir != "" {
		path, err := absoluteExistingFile(filepath.Join(options.configDir, words.Paths.FileName))
		return path, words.Sources.FlagDirectory, err
	}
	if directory := os.Getenv(words.Environment.ConfigDir); directory != "" {
		path, err := absoluteExistingFile(filepath.Join(directory, words.Paths.FileName))
		return path, words.Sources.EnvironmentDirectory, err
	}
	path, err := absoluteExistingFile(words.Paths.DefaultConfig)
	return path, words.Sources.System, err
}

func absoluteExistingFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New(words.Diagnostics.ConfigLookupFailed)
	}
	return filepath.Abs(path)
}

func explainJSON(words config.CLIWords, report config.ExplainReport) map[string]any {
	listeners := make([]any, 0, len(report.Listeners))
	for _, listener := range report.Listeners {
		listeners = append(listeners, map[string]any{
			words.JSON.Name: listener.Name, words.JSON.Type: listener.Type, words.JSON.Address: listener.Address,
		})
	}
	routes := make([]any, 0, len(report.Routes))
	for _, route := range report.Routes {
		routes = append(routes, map[string]any{
			words.JSON.Listener: route.Listener, words.JSON.Index: route.Index, words.JSON.Site: route.Site,
		})
	}
	sites := make([]any, 0, len(report.Sites))
	for _, site := range report.Sites {
		sites = append(sites, map[string]any{
			words.JSON.Name: site.Name, words.JSON.Index: site.Index,
		})
	}
	issues := make([]any, 0, len(report.Issues))
	for _, issue := range report.Issues {
		issues = append(issues, map[string]any{
			words.JSON.Kind: issue.Kind, words.JSON.Name: issue.Name,
		})
	}
	return map[string]any{
		words.JSON.Listeners: listeners,
		words.JSON.Routes:    routes,
		words.JSON.Sites:     sites,
		words.JSON.Issues:    issues,
	}
}

func diffSecond(options options) (string, error) {
	if len(options.command) > 3 {
		return absoluteExistingFile(options.command[3])
	}
	path, _, err := discoverConfig(options)
	return path, err
}

func printDiffChange(words config.CLIWords, kind string, change config.DiffChange) string {
	if change.Name == "" {
		return fmt.Sprintf(words.Diff.Section, kind, change.Section)
	}
	return fmt.Sprintf(words.Diff.Entry, kind, change.Section, change.Name)
}

func diffJSON(words config.CLIWords, report config.DiffReport) map[string]any {
	group := func(changes []config.DiffChange) []any {
		items := make([]any, 0, len(changes))
		for _, change := range changes {
			entry := map[string]any{words.JSON.Section: change.Section}
			if change.Name != "" {
				entry[words.JSON.Name] = change.Name
			}
			items = append(items, entry)
		}
		return items
	}
	return map[string]any{
		words.JSON.Added:   group(report.Added),
		words.JSON.Removed: group(report.Removed),
		words.JSON.Changed: group(report.Changed),
	}
}

func configValidationFailure(output string, err error) int {
	code := words.Codes.ConfigInvalid
	detail := words.Diagnostics.ConfigInvalid
	if compileProblem, ok := config.ProblemFrom(err); ok {
		code = compileProblem.Code
		detail = compileProblem.Detail
	}
	if config.IsUnknownField(err) {
		code = words.Codes.UnknownField
	}
	writeFailure(output, words.Exits.Validation, code, detail)
	return words.Exits.Validation
}

func writeSuccess(output string, value map[string]any) {
	if output == words.Outputs.JSON {
		_ = json.NewEncoder(os.Stdout).Encode(value)
		return
	}
	fmt.Println(words.Text.OK)
}

func writeFailure(output string, exitCode int, code, detail string) {
	if output == words.Outputs.JSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			words.JSON.OK: false,
			words.JSON.Problem: map[string]any{
				words.JSON.Code:   code,
				words.JSON.Detail: detail,
			},
		})
		return
	}
	fmt.Fprintln(os.Stderr, detail)
}
