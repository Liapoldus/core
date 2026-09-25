package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	assets "github.com/Liapoldus/core"
)

type ExternalOptions struct {
	Binary          string
	ExpectedBuildID string
	StateDirectory  string
	StartTimeout    time.Duration
	StopTimeout     time.Duration
	RestartMinimum  time.Duration
	RestartMaximum  time.Duration
	HealthPoll      time.Duration
}

type externalContract struct {
	VersionArgs  []string `json:"versionArgs"`
	AdaptArgs    []string `json:"adaptArgs"`
	RunArgs      []string `json:"runArgs"`
	Placeholders struct {
		Caddyfile string `json:"caddyfile"`
		Config    string `json:"config"`
	} `json:"placeholders"`
	Configuration struct {
		Admin       string `json:"admin"`
		Disabled    string `json:"disabled"`
		Listen      string `json:"listen"`
		AdminConfig string `json:"adminConfig"`
		Persist     string `json:"persist"`
		UnixPrefix  string `json:"unixPrefix"`
		HTTPScheme  string `json:"httpScheme"`
		HTTPHost    string `json:"httpHost"`
		HealthPath  string `json:"healthPath"`
		LoadPath    string `json:"loadPath"`
		LoadMethod  string `json:"loadMethod"`
	} `json:"configuration"`
	Files struct {
		RuntimeDirectory              string `json:"runtimeDirectory"`
		ActiveConfiguration           string `json:"activeConfiguration"`
		AdminSocket                   string `json:"adminSocket"`
		CandidateConfigurationPattern string `json:"candidateConfigurationPattern"`
		CandidateCaddyfilePattern     string `json:"candidateCaddyfilePattern"`
	} `json:"files"`
	Timeouts struct {
		Start          string `json:"start"`
		Stop           string `json:"stop"`
		RestartMinimum string `json:"restartMinimum"`
		RestartMaximum string `json:"restartMaximum"`
		HealthPoll     string `json:"healthPoll"`
	} `json:"timeouts"`
	Modes struct {
		Directory     uint32 `json:"directory"`
		Socket        uint32 `json:"socket"`
		Configuration uint32 `json:"configuration"`
	} `json:"modes"`
	Diagnostics struct {
		InvalidContract       string `json:"invalidContract"`
		BuildIdentityMismatch string `json:"buildIdentityMismatch"`
		AdaptFailed           string `json:"adaptFailed"`
		InvalidConfiguration  string `json:"invalidConfiguration"`
		StartFailed           string `json:"startFailed"`
		NotReady              string `json:"notReady"`
		ActivationFailed      string `json:"activationFailed"`
		RuntimeStopped        string `json:"runtimeStopped"`
	} `json:"diagnostics"`
}

type ExternalRuntime struct {
	mu             sync.Mutex
	contract       externalContract
	binary         string
	expectedBuild  string
	directory      string
	configPath     string
	socketPath     string
	activeConfig   []byte
	child          *exec.Cmd
	stopped        bool
	fenced         bool
	startTimeout   time.Duration
	stopTimeout    time.Duration
	restartMinimum time.Duration
	restartMaximum time.Duration
	healthPoll     time.Duration
	client         *http.Client
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
}

func StartExternal(ctx context.Context, options ExternalOptions, initialCaddyfile []byte) (*ExternalRuntime, error) {
	contract, err := loadExternalContract()
	if err != nil {
		return nil, err
	}
	if ctx == nil || options.Binary == "" || options.ExpectedBuildID == "" || options.StateDirectory == "" || len(initialCaddyfile) == 0 {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	if err := validateExternalContract(contract); err != nil {
		return nil, err
	}
	startTimeout, err := durationOrDefault(options.StartTimeout, contract.Timeouts.Start)
	if err != nil {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	stopTimeout, err := durationOrDefault(options.StopTimeout, contract.Timeouts.Stop)
	if err != nil {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	restartMinimum, err := durationOrDefault(options.RestartMinimum, contract.Timeouts.RestartMinimum)
	if err != nil {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	restartMaximum, err := durationOrDefault(options.RestartMaximum, contract.Timeouts.RestartMaximum)
	if err != nil || restartMaximum < restartMinimum {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	healthPoll, err := durationOrDefault(options.HealthPoll, contract.Timeouts.HealthPoll)
	if err != nil {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	binary, err := filepath.Abs(options.Binary)
	if err != nil {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	stateDirectory, err := filepath.Abs(options.StateDirectory)
	if err != nil {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	if err := verifyExternalBuild(ctx, binary, options.ExpectedBuildID, contract); err != nil {
		return nil, err
	}
	runtimeDirectory := filepath.Join(stateDirectory, contract.Files.RuntimeDirectory)
	if err := os.MkdirAll(runtimeDirectory, os.FileMode(contract.Modes.Directory)); err != nil {
		return nil, errors.New(contract.Diagnostics.StartFailed)
	}
	if err := os.Chmod(runtimeDirectory, os.FileMode(contract.Modes.Directory)); err != nil {
		return nil, errors.New(contract.Diagnostics.StartFailed)
	}
	runtimeContext, cancel := context.WithCancel(context.Background())
	runtime := &ExternalRuntime{
		contract: contract, binary: binary, expectedBuild: options.ExpectedBuildID,
		directory: runtimeDirectory, configPath: filepath.Join(runtimeDirectory, contract.Files.ActiveConfiguration),
		socketPath:   filepath.Join(runtimeDirectory, contract.Files.AdminSocket),
		startTimeout: startTimeout, stopTimeout: stopTimeout, restartMinimum: restartMinimum,
		restartMaximum: restartMaximum, healthPoll: healthPoll, ctx: runtimeContext, cancel: cancel,
		done: make(chan struct{}),
	}
	runtime.client = runtime.makeAdminClient()
	prepared, err := runtime.prepare(ctx, initialCaddyfile)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := runtime.writeActiveConfig(prepared); err != nil {
		cancel()
		return nil, errors.New(contract.Diagnostics.StartFailed)
	}
	runtime.activeConfig = append([]byte(nil), prepared...)
	child, err := runtime.startChild(prepared)
	if err != nil {
		cancel()
		return nil, err
	}
	runtime.child = child
	go runtime.supervise(child)
	if err := runtime.waitReady(ctx, startTimeout); err != nil {
		_ = runtime.Stop()
		return nil, errors.New(contract.Diagnostics.NotReady)
	}
	return runtime, nil
}

func (runtime *ExternalRuntime) Validate(ctx context.Context, caddyfile []byte) error {
	_, err := runtime.prepare(ctx, caddyfile)
	return err
}

func (runtime *ExternalRuntime) Activate(ctx context.Context, caddyfile []byte) error {
	if runtime == nil {
		contract, err := loadExternalContract()
		if err != nil {
			return err
		}
		return errors.New(contract.Diagnostics.RuntimeStopped)
	}
	candidate, err := runtime.prepare(ctx, caddyfile)
	if err != nil {
		return err
	}
	requestContext, cancel := context.WithTimeout(ctx, runtime.startTimeout)
	defer cancel()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.stopped || runtime.child == nil {
		return errors.New(runtime.contract.Diagnostics.RuntimeStopped)
	}
	response, err := runtime.sendSnapshot(requestContext, candidate)
	if err != nil {
		runtime.fenced = true
		return errors.New(runtime.contract.Diagnostics.ActivationFailed)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New(runtime.contract.Diagnostics.ActivationFailed)
	}
	if err := runtime.writeActiveConfig(candidate); err != nil {
		previous := append([]byte(nil), runtime.activeConfig...)
		if previousResponse, rollbackErr := runtime.sendSnapshot(requestContext, previous); rollbackErr != nil {
			runtime.fenced = true
		} else {
			_ = previousResponse.Body.Close()
		}
		return errors.New(runtime.contract.Diagnostics.ActivationFailed)
	}
	runtime.activeConfig = append(runtime.activeConfig[:0], candidate...)
	runtime.fenced = false
	return nil
}

func (runtime *ExternalRuntime) Ready(ctx context.Context) error {
	if runtime == nil {
		contract, err := loadExternalContract()
		if err != nil {
			return err
		}
		return errors.New(contract.Diagnostics.RuntimeStopped)
	}
	runtime.mu.Lock()
	stopped, fenced, child := runtime.stopped, runtime.fenced, runtime.child
	runtime.mu.Unlock()
	if stopped {
		return errors.New(runtime.contract.Diagnostics.RuntimeStopped)
	}
	if fenced || child == nil {
		return errors.New(runtime.contract.Diagnostics.NotReady)
	}
	requestContext, cancel := context.WithTimeout(ctx, runtime.startTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, runtime.adminURL(runtime.contract.Configuration.HealthPath), nil)
	if err != nil {
		return errors.New(runtime.contract.Diagnostics.NotReady)
	}
	response, err := runtime.client.Do(request)
	if err != nil {
		return errors.New(runtime.contract.Diagnostics.NotReady)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New(runtime.contract.Diagnostics.NotReady)
	}
	return nil
}

func (runtime *ExternalRuntime) AdminSocketPath() string {
	if runtime == nil {
		return ""
	}
	return runtime.socketPath
}

func (runtime *ExternalRuntime) Stop() error {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	if runtime.stopped {
		done := runtime.done
		runtime.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-time.After(runtime.stopTimeout):
			return errors.New(runtime.contract.Diagnostics.StartFailed)
		}
	}
	runtime.stopped = true
	runtime.cancel()
	child := runtime.child
	runtime.mu.Unlock()
	if child != nil && child.Process != nil {
		_ = child.Process.Signal(os.Interrupt)
	}
	select {
	case <-runtime.done:
		return nil
	case <-time.After(runtime.stopTimeout):
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
		}
		select {
		case <-runtime.done:
			return nil
		case <-time.After(runtime.stopTimeout):
			return errors.New(runtime.contract.Diagnostics.StartFailed)
		}
	}
}

func (runtime *ExternalRuntime) supervise(initial *exec.Cmd) {
	defer close(runtime.done)
	child := initial
	delay := runtime.restartMinimum
	for {
		_ = child.Wait()
		runtime.mu.Lock()
		if runtime.child == child {
			runtime.child = nil
		}
		stopped := runtime.stopped
		runtime.mu.Unlock()
		if stopped {
			return
		}
		for {
			timer := time.NewTimer(delay)
			select {
			case <-runtime.ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
			runtime.mu.Lock()
			if runtime.stopped {
				runtime.mu.Unlock()
				return
			}
			candidate := append([]byte(nil), runtime.activeConfig...)
			started, err := runtime.startChild(candidate)
			if err == nil {
				runtime.child = started
			}
			runtime.mu.Unlock()
			if err != nil {
				delay = runtime.nextRestartDelay(delay)
				continue
			}
			readyContext, cancel := context.WithTimeout(runtime.ctx, runtime.startTimeout)
			readyErr := runtime.waitReady(readyContext, runtime.startTimeout)
			cancel()
			if readyErr == nil {
				runtime.mu.Lock()
				runtime.fenced = false
				runtime.mu.Unlock()
				child = started
				delay = runtime.restartMinimum
				break
			}
			if started.Process != nil {
				_ = started.Process.Signal(os.Interrupt)
			}
			child = started
			delay = runtime.nextRestartDelay(delay)
			break
		}
		if runtime.ctx.Err() != nil {
			return
		}
	}
}

func (runtime *ExternalRuntime) nextRestartDelay(current time.Duration) time.Duration {
	if current >= runtime.restartMaximum/2 {
		return runtime.restartMaximum
	}
	return current * 2
}

func (runtime *ExternalRuntime) waitReady(ctx context.Context, timeout time.Duration) error {
	readinessContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(runtime.healthPoll)
	defer ticker.Stop()
	for {
		if err := runtime.Ready(readinessContext); err == nil {
			if err := os.Chmod(runtime.socketPath, os.FileMode(runtime.contract.Modes.Socket)); err != nil {
				return err
			}
			return nil
		}
		select {
		case <-readinessContext.Done():
			return readinessContext.Err()
		case <-ticker.C:
		}
	}
}

func (runtime *ExternalRuntime) startChild(configuration []byte) (*exec.Cmd, error) {
	if err := runtime.writeActiveConfig(configuration); err != nil {
		return nil, errors.New(runtime.contract.Diagnostics.StartFailed)
	}
	arguments := expandExternalArgs(runtime.contract.RunArgs, runtime.contract.Placeholders.Config, runtime.configPath)
	child := exec.Command(runtime.binary, arguments...)
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		return nil, errors.New(runtime.contract.Diagnostics.StartFailed)
	}
	return child, nil
}

func (runtime *ExternalRuntime) prepare(ctx context.Context, caddyfile []byte) ([]byte, error) {
	if runtime == nil || len(caddyfile) == 0 {
		if runtime == nil {
			contract, err := loadExternalContract()
			if err != nil {
				return nil, err
			}
			return nil, errors.New(contract.Diagnostics.RuntimeStopped)
		}
		return nil, errors.New(runtime.contract.Diagnostics.AdaptFailed)
	}
	source, err := os.CreateTemp(runtime.directory, runtime.contract.Files.CandidateCaddyfilePattern)
	if err != nil {
		return nil, errors.New(runtime.contract.Diagnostics.AdaptFailed)
	}
	sourcePath := source.Name()
	defer os.Remove(sourcePath)
	if err := source.Chmod(os.FileMode(runtime.contract.Modes.Configuration)); err != nil {
		_ = source.Close()
		return nil, errors.New(runtime.contract.Diagnostics.AdaptFailed)
	}
	if _, err := source.Write(caddyfile); err != nil {
		_ = source.Close()
		return nil, errors.New(runtime.contract.Diagnostics.AdaptFailed)
	}
	if err := source.Close(); err != nil {
		return nil, errors.New(runtime.contract.Diagnostics.AdaptFailed)
	}
	arguments := expandExternalArgs(runtime.contract.AdaptArgs, runtime.contract.Placeholders.Caddyfile, sourcePath)
	command := exec.CommandContext(ctx, runtime.binary, arguments...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, errors.New(runtime.contract.Diagnostics.AdaptFailed)
	}
	var configuration map[string]any
	if err := json.Unmarshal(output.Bytes(), &configuration); err != nil {
		return nil, errors.New(runtime.contract.Diagnostics.InvalidConfiguration)
	}
	admin, ok := objectAt(configuration, runtime.contract.Configuration.Admin, true)
	if !ok {
		return nil, errors.New(runtime.contract.Diagnostics.InvalidConfiguration)
	}
	admin[runtime.contract.Configuration.Disabled] = false
	admin[runtime.contract.Configuration.Listen] = runtime.contract.Configuration.UnixPrefix + runtime.socketPath
	adminConfig, ok := objectAt(admin, runtime.contract.Configuration.AdminConfig, true)
	if !ok {
		return nil, errors.New(runtime.contract.Diagnostics.InvalidConfiguration)
	}
	adminConfig[runtime.contract.Configuration.Persist] = false
	prepared, err := json.Marshal(configuration)
	if err != nil {
		return nil, errors.New(runtime.contract.Diagnostics.InvalidConfiguration)
	}
	return prepared, nil
}

func (runtime *ExternalRuntime) sendSnapshot(ctx context.Context, configuration []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, runtime.contract.Configuration.LoadMethod, runtime.adminURL(runtime.contract.Configuration.LoadPath), bytes.NewReader(configuration))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	return runtime.client.Do(request)
}

func (runtime *ExternalRuntime) adminURL(path string) string {
	return runtime.contract.Configuration.HTTPScheme + "://" + runtime.contract.Configuration.HTTPHost + path
}

func (runtime *ExternalRuntime) makeAdminClient() *http.Client {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", runtime.socketPath)
		},
	}
	return &http.Client{Transport: transport}
}

func (runtime *ExternalRuntime) writeActiveConfig(configuration []byte) error {
	temporary, err := os.CreateTemp(runtime.directory, runtime.contract.Files.CandidateConfigurationPattern)
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(os.FileMode(runtime.contract.Modes.Configuration)); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(configuration); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, runtime.configPath)
}

func loadExternalContract() (externalContract, error) {
	contents, err := assets.Contract(assets.CaddyExternal)
	if err != nil {
		return externalContract{}, err
	}
	var contract externalContract
	if err := json.Unmarshal(contents, &contract); err != nil {
		return externalContract{}, err
	}
	if err := validateExternalContract(contract); err != nil {
		return externalContract{}, err
	}
	return contract, nil
}

func validateExternalContract(contract externalContract) error {
	if len(contract.VersionArgs) == 0 || len(contract.AdaptArgs) == 0 || len(contract.RunArgs) == 0 ||
		contract.Placeholders.Caddyfile == "" || contract.Placeholders.Config == "" ||
		contract.Configuration.Admin == "" || contract.Configuration.Disabled == "" || contract.Configuration.Listen == "" ||
		contract.Configuration.AdminConfig == "" || contract.Configuration.Persist == "" || contract.Configuration.UnixPrefix == "" ||
		contract.Configuration.HTTPScheme == "" || contract.Configuration.HTTPHost == "" || contract.Configuration.HealthPath == "" ||
		contract.Configuration.LoadPath == "" || contract.Configuration.LoadMethod == "" ||
		contract.Files.RuntimeDirectory == "" || contract.Files.ActiveConfiguration == "" || contract.Files.AdminSocket == "" ||
		contract.Files.CandidateConfigurationPattern == "" || contract.Files.CandidateCaddyfilePattern == "" ||
		contract.Modes.Directory == 0 || contract.Modes.Socket == 0 || contract.Modes.Configuration == 0 ||
		contract.Diagnostics.InvalidContract == "" || contract.Diagnostics.BuildIdentityMismatch == "" || contract.Diagnostics.AdaptFailed == "" ||
		contract.Diagnostics.InvalidConfiguration == "" || contract.Diagnostics.StartFailed == "" || contract.Diagnostics.NotReady == "" ||
		contract.Diagnostics.ActivationFailed == "" || contract.Diagnostics.RuntimeStopped == "" {
		return errors.New(contract.Diagnostics.InvalidContract)
	}
	return nil
}

func verifyExternalBuild(ctx context.Context, binary, expected string, contract externalContract) error {
	command := exec.CommandContext(ctx, binary, contract.VersionArgs...)
	command.Stderr = io.Discard
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != expected {
		return errors.New(contract.Diagnostics.BuildIdentityMismatch)
	}
	return nil
}

func durationOrDefault(value time.Duration, defaultValue string) (time.Duration, error) {
	if value > 0 {
		return value, nil
	}
	return time.ParseDuration(defaultValue)
}

func expandExternalArgs(arguments []string, placeholder, value string) []string {
	expanded := make([]string, len(arguments))
	for index, argument := range arguments {
		expanded[index] = strings.ReplaceAll(argument, placeholder, value)
	}
	return expanded
}

func objectAt(parent map[string]any, key string, create bool) (map[string]any, bool) {
	value, exists := parent[key]
	if !exists && !create {
		return nil, false
	}
	if !exists {
		value = make(map[string]any)
		parent[key] = value
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return object, true
}
