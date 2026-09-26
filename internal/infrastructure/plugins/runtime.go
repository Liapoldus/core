package plugins

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/pluginprotocol/transport"
)

var ErrPluginStartup = errors.New("plugin startup failed")

type runningInstance struct {
	name             string
	endpoint         string
	grantServer      *transport.GrantServer
	grantListener    net.Listener
	broker           *grantBroker
	listener         *net.TCPListener
	client           *Client
	capability       *CapabilityClient
	model            models.PluginInstance
	spec             Spec
	done             <-chan error
	resourceExceeded *atomic.Bool
}

type Runtime struct {
	supervisor  *Supervisor
	instances   map[string]runningInstance
	cancel      context.CancelFunc
	watchCancel context.CancelFunc
}

type DispatchBinding struct {
	Name               string
	Endpoint           string
	Timeout            time.Duration
	StartTimeout       time.Duration
	MaxConcurrentCalls int
}

func StartRuntime(ctx context.Context, configured map[string]models.PluginInstance, secrets map[string]models.Secret) (*Runtime, error) {
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
		listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: netip.AddrFrom4([4]byte{127, 0, 0, 1}).AsSlice()})
		if err != nil {
			clearConfigSecrets(runtimeConfigSecrets)
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		endpoint := listener.Addr().String()
		grantListener, err := net.Listen("tcp", net.JoinHostPort(netip.AddrFrom4([4]byte{127, 0, 0, 1}).String(), strconv.Itoa(0)))
		if err != nil {
			clearConfigSecrets(runtimeConfigSecrets)
			_ = listener.Close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		broker := newGrantBroker(name, instance.SettingsRevision, instance.SecretGrants, secrets, runtimeConfigSecrets, instance.ConfigGrantPurpose)
		brokerServer := transport.NewGrantBrokerServer(broker)
		go func() { _ = brokerServer.Serve(grantListener) }()
		spec := Spec{
			Instance: name,
			Binary:   instance.Binary,
			Listener: listener,
			Restart: RestartPolicy{
				Enabled: instance.RestartEnabled,
				Initial: instance.RestartInitialBackoff,
				Max:     instance.RestartMaximumBackoff,
			},
		}
		done, err := runtime.supervisor.StartWithExit(processContext, spec)
		if err != nil {
			brokerServer.Stop()
			_ = grantListener.Close()
			_ = listener.Close()
			broker.close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		client, err := NewClient(endpoint, instance.Timeout, instance.StartTimeout)
		if err != nil {
			brokerServer.Stop()
			_ = grantListener.Close()
			_ = listener.Close()
			broker.close()
			_ = runtime.supervisor.Stop(name)
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		configGrants, configHandles, err := broker.issueConfig()
		if err != nil {
			brokerServer.Stop()
			_ = grantListener.Close()
			_ = listener.Close()
			_ = client.Close()
			_ = runtime.supervisor.Stop(name)
			broker.close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		handshake, err := client.BootstrapAndHandshake(ctx, name, grantListener.Addr().String(), instance.Settings, instance.SettingsRevision, configGrants)
		broker.revoke(configHandles)
		if err != nil || handshake.Manifest.GetName() != name || !manifestIncludes(handshake.Manifest.GetCapabilities(), instance.Capabilities) {
			brokerServer.Stop()
			_ = grantListener.Close()
			_ = listener.Close()
			_ = client.Close()
			_ = runtime.supervisor.Stop(name)
			broker.close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		capability, err := NewCapabilityClient(client, instance.MaxConcurrentCalls, instance.Capabilities...)
		if err != nil {
			brokerServer.Stop()
			_ = grantListener.Close()
			_ = listener.Close()
			_ = client.Close()
			_ = runtime.supervisor.Stop(name)
			broker.close()
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		resourceExceeded := &atomic.Bool{}
		capability.grantBroker = broker
		capability.setRSSLimit(instance.MemoryLimitBytes, func() (uint64, error) {
			return runtime.supervisor.ResidentMemory(name)
		}, func() {
			_ = runtime.supervisor.Stop(name)
		}, resourceExceeded)
		runtime.instances[name] = runningInstance{name: name, endpoint: endpoint, grantServer: brokerServer, grantListener: grantListener, broker: broker, listener: listener, client: client, capability: capability, model: instance, spec: spec, done: done, resourceExceeded: resourceExceeded}
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
			instance.resourceExceeded.Store(true)
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
		done, err := r.supervisor.StartWithExit(processContext, instance.spec)
		if err == nil {
			instance.resourceExceeded.Store(false)
			configGrants, configHandles, grantErr := instance.broker.issueConfig()
			if grantErr != nil {
				_ = r.supervisor.Stop(instance.name)
				select {
				case <-ctx.Done():
					return nil
				case <-done:
				}
				continue
			}
			reconnectErr := instance.client.Reconnect(ctx, instance.endpoint, instance.name, instance.grantListener.Addr().String(), instance.model.Settings, instance.model.SettingsRevision, configGrants, instance.name, instance.model.Capabilities)
			instance.broker.revoke(configHandles)
			if reconnectErr == nil {
				return done
			}
			_ = r.supervisor.Stop(instance.name)
			select {
			case <-ctx.Done():
				return nil
			case <-done:
			}
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

func (r *Runtime) HTTPDispatchers() map[string]*CapabilityClient {
	instances := make(map[string]*CapabilityClient, len(r.instances))
	for name, instance := range r.instances {
		instances[name] = instance.capability
	}
	return instances
}

func (r *Runtime) L4Dispatchers() map[string]*CapabilityClient {
	instances := make(map[string]*CapabilityClient, len(r.instances))
	for name, instance := range r.instances {
		instances[name] = instance.capability
	}
	return instances
}

func (r *Runtime) DispatchBindings() []DispatchBinding {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.instances))
	for name := range r.instances {
		names = append(names, name)
	}
	sort.Strings(names)
	bindings := make([]DispatchBinding, 0, len(names))
	for _, name := range names {
		instance := r.instances[name]
		bindings = append(bindings, DispatchBinding{
			Name: instance.name, Endpoint: instance.endpoint,
			Timeout: instance.model.Timeout, StartTimeout: instance.model.StartTimeout,
			MaxConcurrentCalls: instance.model.MaxConcurrentCalls,
		})
	}
	return bindings
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

func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.watchCancel != nil {
		r.watchCancel()
	}
	var failures []error
	for name, instance := range r.instances {
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
		if instance.grantListener != nil {
			_ = instance.grantListener.Close()
		}
		if instance.listener != nil {
			_ = instance.listener.Close()
		}
	}
	if r.cancel != nil {
		r.cancel()
	}
	return errors.Join(failures...)
}
