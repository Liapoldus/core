package plugins

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/pluginprotocol/transport"
)

var ErrPluginStartup = errors.New("plugin startup failed")

type runningInstance struct {
	client     *Client
	capability *CapabilityClient
	model      models.PluginInstance
}

type Runtime struct {
	supervisor *Supervisor
	instances  map[string]runningInstance
	cancel     context.CancelFunc
}

func StartRuntime(ctx context.Context, configured map[string]models.PluginInstance) (*Runtime, error) {
	processContext, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{supervisor: NewSupervisor(), instances: make(map[string]runningInstance, len(configured)), cancel: cancel}
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		instance := configured[name]
		endpoint, err := reserveLoopbackEndpoint()
		if err != nil {
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		env, err := pluginEnvironment(instance.Env, endpoint)
		if err != nil {
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		if err := runtime.supervisor.Start(processContext, Spec{Instance: name, Binary: instance.Binary, Args: instance.Args, Env: env}); err != nil {
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		client, err := NewClient(endpoint, instance.Timeout)
		if err != nil {
			_ = runtime.supervisor.Stop(name)
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		handshake, err := client.Handshake(ctx, instance.Settings)
		if err != nil || handshake.Manifest.GetName() != name || !manifestIncludes(handshake.Manifest.GetCapabilities(), instance.Capabilities) {
			_ = client.Close()
			_ = runtime.supervisor.Stop(name)
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		capability, err := NewCapabilityClient(client, instance.MaxConcurrentCalls, instance.Capabilities...)
		if err != nil {
			_ = client.Close()
			_ = runtime.supervisor.Stop(name)
			_ = runtime.Stop(context.Background())
			return nil, ErrPluginStartup
		}
		runtime.instances[name] = runningInstance{client: client, capability: capability, model: instance}
	}
	if err := ctx.Err(); err != nil {
		_ = runtime.Stop(context.Background())
		return nil, ErrPluginStartup
	}
	return runtime, nil
}

func reserveLoopbackEndpoint() (string, error) {
	host := netip.AddrFrom4([4]byte{127, 0, 0, 1}).String()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(0)))
	if err != nil {
		return "", err
	}
	endpoint := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return endpoint, nil
}

func pluginEnvironment(configured []string, endpoint string) ([]string, error) {
	result := make([]string, 0, len(configured)+1)
	for _, value := range configured {
		key, _, hasValue := strings.Cut(value, "=")
		if hasValue && key == transport.EndpointEnvironment {
			return nil, ErrPluginStartup
		}
		result = append(result, value)
	}
	return append(result, transport.EndpointEnvironment+"="+endpoint), nil
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

func (r *Runtime) IdentityDispatchers() map[string]*CapabilityClient {
	instances := make(map[string]*CapabilityClient, len(r.instances))
	for name, instance := range r.instances {
		instances[name] = instance.capability
	}
	return instances
}

func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
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
	}
	if r.cancel != nil {
		r.cancel()
	}
	return errors.Join(failures...)
}
