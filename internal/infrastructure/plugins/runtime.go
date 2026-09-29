package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	pluginsdk "github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

var ErrPluginStartup = errors.New("plugin startup failed")

type runningInstance struct {
	name            string
	endpoint        string
	grantServer     *pluginsdk.StartedGrantServer
	broker          *grantBroker
	listener        *pluginsdk.LocalListener
	client          *Client
	model           models.PluginInstance
	spec            Spec
	done            <-chan error
	configurationMu *sync.Mutex
}

type Runtime struct {
	mu          sync.RWMutex
	supervisor  *Supervisor
	instances   map[string]runningInstance
	settings    RuntimeSettings
	cancel      context.CancelFunc
	watchCancel context.CancelFunc
}

type RuntimeSettings struct {
	FileReferencePrefix string
	MaximumSecretBytes  int64
}

var _ interfaces.PluginConfigurationApplier = (*Runtime)(nil)

func StartRuntime(ctx context.Context, configured map[string]models.PluginInstance, settings ...RuntimeSettings) (*Runtime, error) {
	defer func() {
		for name, instance := range configured {
			clearConfigSecrets(instance.ConfigGrantSecrets)
			instance.ConfigGrantSecrets = nil
			configured[name] = instance
		}
	}()
	processContext, cancel := context.WithCancel(context.Background())
	watchContext, watchCancel := context.WithCancel(ctx)
	runtime := &Runtime{supervisor: NewSupervisor(), instances: make(map[string]runningInstance, len(configured)), cancel: cancel, watchCancel: watchCancel}
	if len(settings) > 0 {
		runtime.settings = settings[0]
	}
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		instance := configured[name]
		runtimeConfigSecrets := cloneConfigSecrets(instance.ConfigGrantSecrets)
		clearConfigSecrets(instance.ConfigGrantSecrets)
		instance.ConfigGrantSecrets = nil
		configured[name] = instance
		listener, err := pluginsdk.ListenLoopback()
		if err != nil {
			clearConfigSecrets(runtimeConfigSecrets)
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		endpoint := listener.Endpoint()
		broker := newGrantBroker(name, instance.SettingsRevision, runtimeConfigSecrets, instance.ConfigGrantPurpose)
		grantServer, err := pluginsdk.StartGrantBroker(broker)
		if err != nil {
			clearConfigSecrets(runtimeConfigSecrets)
			broker.close()
			_ = listener.Close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		spec := Spec{
			Instance:     name,
			Binary:       instance.Binary,
			ListenerFile: listener.File,
			Restart: RestartPolicy{
				Enabled: instance.RestartEnabled,
				Initial: instance.RestartInitialBackoff,
				Max:     instance.RestartMaximumBackoff,
			},
		}
		done, err := runtime.supervisor.StartWithExit(processContext, spec)
		if err != nil {
			grantServer.Stop()
			_ = listener.Close()
			broker.close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		client, err := NewClient(endpoint, instance.Timeout, instance.StartTimeout)
		if err != nil {
			grantServer.Stop()
			_ = listener.Close()
			broker.close()
			_ = runtime.supervisor.Stop(name)
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		configGrants, configHandles, err := broker.issueConfig()
		if err != nil {
			grantServer.Stop()
			_ = listener.Close()
			_ = client.Close()
			_ = runtime.supervisor.Stop(name)
			broker.close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		handshake, err := client.BootstrapAndHandshake(ctx, name, grantServer.Endpoint(), instance.Settings, instance.SettingsRevision, configGrants)
		broker.revoke(configHandles)
		if err != nil || handshake.Manifest.GetName() != name || !manifestIncludes(handshake.Manifest.GetCapabilities(), instance.Capabilities) {
			grantServer.Stop()
			_ = listener.Close()
			_ = client.Close()
			_ = runtime.supervisor.Stop(name)
			broker.close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		runtime.instances[name] = runningInstance{name: name, endpoint: endpoint, grantServer: grantServer, broker: broker, listener: listener, client: client, model: instance, spec: spec, done: done, configurationMu: &sync.Mutex{}}
	}
	if err := ctx.Err(); err != nil {
		_ = runtime.Stop(context.Background())
		return nil, ErrPluginStartup
	}
	for _, instance := range runtime.instances {
		go runtime.supervise(watchContext, processContext, instance)
	}
	return runtime, nil
}

func (r *Runtime) supervise(ctx, processContext context.Context, instance runningInstance) {
	ticker := time.NewTicker(instance.model.HealthProbeInterval)
	defer ticker.Stop()
	memoryTicker := time.NewTicker(instance.model.MemoryProbeInterval)
	defer memoryTicker.Stop()
	done := instance.done
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			if ctx.Err() != nil || !instance.spec.Restart.Enabled {
				return
			}
			done = r.restartUntilReady(ctx, processContext, instance)
			if done == nil {
				return
			}
			failures = 0
		case <-ticker.C:
			healthContext, cancel := context.WithTimeout(ctx, instance.model.Timeout)
			err := instance.client.CheckHealth(healthContext)
			cancel()
			if err == nil {
				failures = 0
				continue
			}
			failures++
			if failures < instance.model.HealthFailureThreshold {
				continue
			}
			if ctx.Err() != nil || !instance.spec.Restart.Enabled {
				return
			}
			_ = r.supervisor.Stop(instance.name)
			select {
			case <-ctx.Done():
				return
			case <-done:
			}
			done = r.restartUntilReady(ctx, processContext, instance)
			if done == nil {
				return
			}
			failures = 0
		case <-memoryTicker.C:
			resident, err := r.supervisor.ResidentMemory(instance.name)
			if err != nil || resident <= instance.model.MemoryLimitBytes {
				continue
			}
			_ = r.supervisor.Stop(instance.name)
			select {
			case <-ctx.Done():
				return
			case <-done:
			}
			if ctx.Err() != nil || !instance.spec.Restart.Enabled {
				return
			}
			done = r.restartUntilReady(ctx, processContext, instance)
			if done == nil {
				return
			}
			failures = 0
		}
	}
}

func (r *Runtime) restartUntilReady(ctx, processContext context.Context, instance runningInstance) <-chan error {
	for attempt := 1; ; attempt++ {
		timer := time.NewTimer(instance.spec.Restart.Delay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		current, exists := r.instance(instance.name)
		if !exists {
			return nil
		}
		lock := current.configurationMu
		lock.Lock()
		current, exists = r.instance(instance.name)
		if !exists {
			lock.Unlock()
			return nil
		}
		done, err := r.supervisor.StartWithExit(processContext, current.spec)
		if err == nil {
			configGrants, configHandles, grantErr := current.broker.issueConfig()
			if grantErr != nil {
				_ = r.supervisor.Stop(current.name)
				lock.Unlock()
				select {
				case <-ctx.Done():
					return nil
				case <-done:
				}
				continue
			}
			reconnectErr := current.client.Reconnect(ctx, current.endpoint, current.name, current.grantServer.Endpoint(), current.model.Settings, current.model.SettingsRevision, configGrants, current.name, current.model.Capabilities)
			current.broker.revoke(configHandles)
			lock.Unlock()
			if reconnectErr == nil {
				return done
			}
			_ = r.supervisor.Stop(current.name)
			select {
			case <-ctx.Done():
				return nil
			case <-done:
			}
		} else {
			lock.Unlock()
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func manifestIncludes(advertised, configured []string) bool {
	available := make(map[string]struct{}, len(advertised))
	for _, capability := range advertised {
		available[capability] = struct{}{}
	}
	for _, capability := range configured {
		if _, exists := available[capability]; !exists {
			return false
		}
	}
	return true
}

func (r *Runtime) ApplyConfiguration(ctx context.Context, instanceID, revision string, configuration []byte) error {
	if r == nil || instanceID == "" || revision == "" || !json.Valid(configuration) {
		return ErrProtocolViolation
	}
	instance, exists := r.instance(instanceID)
	if !exists {
		return ErrPluginUnavailable
	}
	instance.configurationMu.Lock()
	defer instance.configurationMu.Unlock()
	instance, exists = r.instance(instanceID)
	if !exists {
		return ErrPluginUnavailable
	}
	prepared, configSecrets, err := prepareRuntimeSettings(configuration, r.settings)
	if err != nil {
		return ErrProtocolViolation
	}
	grants, handles, err := instance.broker.issueConfigFor(revision, configSecrets)
	if err != nil {
		clearConfigSecrets(configSecrets)
		return err
	}
	err = instance.client.ApplyConfiguration(ctx, revision, prepared, grants)
	instance.broker.revoke(handles)
	if err != nil {
		clearConfigSecrets(configSecrets)
		if errors.Is(err, ErrProtocolViolation) && r.reapplyActiveConfiguration(ctx, instance) != nil {
			return ErrPluginUnavailable
		}
		return err
	}
	instance.broker.replaceConfiguration(revision, configSecrets)
	clearConfigSecrets(configSecrets)
	instance.model.Settings = append([]byte(nil), prepared...)
	instance.model.SettingsRevision = revision
	r.mu.Lock()
	if _, exists := r.instances[instanceID]; exists {
		r.instances[instanceID] = instance
	}
	r.mu.Unlock()
	return nil
}

func (r *Runtime) reapplyActiveConfiguration(ctx context.Context, instance runningInstance) error {
	grants, handles, err := instance.broker.issueConfig()
	if err != nil {
		return ErrPluginUnavailable
	}
	defer instance.broker.revoke(handles)

	return instance.client.Reconnect(
		ctx, instance.endpoint, instance.name, instance.grantServer.Endpoint(),
		instance.model.Settings, instance.model.SettingsRevision, grants,
		instance.name, instance.model.Capabilities,
	)
}

func (r *Runtime) instance(name string) (runningInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	instance, exists := r.instances[name]
	return instance, exists
}

func cloneConfigSecrets(secrets map[string][]byte) map[string][]byte {
	if len(secrets) == 0 {
		return nil
	}
	cloned := make(map[string][]byte, len(secrets))
	for reference, secret := range secrets {
		cloned[reference] = append([]byte(nil), secret...)
	}
	return cloned
}

func prepareRuntimeSettings(configuration []byte, settings RuntimeSettings) ([]byte, map[string][]byte, error) {
	var document map[string]json.RawMessage
	if json.Unmarshal(configuration, &document) != nil || document == nil {
		return nil, nil, ErrProtocolViolation
	}
	if settings.FileReferencePrefix == "" {
		return append([]byte(nil), configuration...), nil, nil
	}
	return preparePluginSettings(configuration, settings.FileReferencePrefix, settings.MaximumSecretBytes)
}

func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.watchCancel != nil {
		r.watchCancel()
	}
	var failures []error
	r.mu.RLock()
	instances := make(map[string]runningInstance, len(r.instances))
	for name, instance := range r.instances {
		instances[name] = instance
	}
	r.mu.RUnlock()
	for name, instance := range instances {
		instance.configurationMu.Lock()
		stopContext, cancel := context.WithTimeout(ctx, instance.model.Timeout)
		_ = instance.client.Shutdown(stopContext)
		cancel()
		if err := instance.client.Close(); err != nil {
			failures = append(failures, ErrPluginUnavailable)
		}
		if err := r.supervisor.Stop(name); err != nil && !errors.Is(err, ErrPluginNotRunning) {
			failures = append(failures, ErrPluginUnavailable)
		}
		if instance.grantServer != nil {
			instance.grantServer.Stop()
		}
		if instance.broker != nil {
			instance.broker.close()
		}
		if instance.listener != nil {
			_ = instance.listener.Close()
		}
		instance.configurationMu.Unlock()
	}
	if r.cancel != nil {
		r.cancel()
	}
	return errors.Join(failures...)
}
