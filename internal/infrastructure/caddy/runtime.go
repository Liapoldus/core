package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	caddyassets "github.com/Liapoldus/core"
	caddycore "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
)

type buildContract struct {
	Adapter     string `json:"adapter"`
	Diagnostics struct {
		AdapterUnavailable   string `json:"adapterUnavailable"`
		RuntimeAlreadyActive string `json:"runtimeAlreadyActive"`
		RuntimeNotActive     string `json:"runtimeNotActive"`
	} `json:"diagnostics"`
}

type Runtime struct {
	active  bool
	plugins []PluginInstance
}

var processRuntime = struct {
	sync.Mutex
	active *Runtime
}{}

func StartCaddyfile(source []byte) (*Runtime, []caddyconfig.Warning, error) {
	return startCaddyfile(source, nil)
}

func StartCaddyfileWithPlugins(source []byte, instances []PluginInstance) (*Runtime, []caddyconfig.Warning, error) {
	return startCaddyfile(source, instances)
}

func startCaddyfile(source []byte, instances []PluginInstance) (*Runtime, []caddyconfig.Warning, error) {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if processRuntime.active != nil {
		contract, err := loadBuildContract()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New(contract.Diagnostics.RuntimeAlreadyActive)
	}

	configuration, warnings, err := adaptCaddyfile(source, instances)
	if err != nil {
		return nil, nil, err
	}
	if err := caddycore.Load(configuration, true); err != nil {
		return nil, nil, err
	}
	runtime := &Runtime{active: true, plugins: append([]PluginInstance(nil), instances...)}
	processRuntime.active = runtime
	return runtime, warnings, nil
}

func (runtime *Runtime) ReplaceCaddyfile(source []byte) ([]caddyconfig.Warning, error) {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if runtime == nil || processRuntime.active != runtime || !runtime.active {
		contract, err := loadBuildContract()
		if err != nil {
			return nil, err
		}
		return nil, errors.New(contract.Diagnostics.RuntimeNotActive)
	}

	configuration, warnings, err := adaptCaddyfile(source, runtime.plugins)
	if err != nil {
		return nil, err
	}
	if err := caddycore.Load(configuration, true); err != nil {
		return nil, err
	}
	return warnings, nil
}

func (runtime *Runtime) Validate(_ context.Context, source []byte) error {
	_, _, err := adaptCaddyfile(source, runtime.plugins)
	return err
}

func (runtime *Runtime) Activate(_ context.Context, source []byte) error {
	_, err := runtime.ReplaceCaddyfile(source)
	return err
}

func AdaptCaddyfile(source []byte) ([]byte, []caddyconfig.Warning, error) {
	return adaptCaddyfile(source, nil)
}

func adaptCaddyfile(source []byte, instances []PluginInstance) ([]byte, []caddyconfig.Warning, error) {
	contract, err := loadBuildContract()
	if err != nil {
		return nil, nil, err
	}
	adapter := caddyconfig.GetAdapter(contract.Adapter)
	if adapter == nil {
		return nil, nil, errors.New(contract.Diagnostics.AdapterUnavailable)
	}
	configuration, warnings, err := adapter.Adapt(source, nil)
	if err != nil {
		return nil, nil, err
	}
	var adapted caddycore.Config
	if err := json.Unmarshal(configuration, &adapted); err != nil {
		return nil, nil, err
	}
	if instances != nil {
		appName, appConfig, err := pluginDispatchAppConfig(instances)
		if err != nil {
			return nil, nil, err
		}
		if adapted.AppsRaw == nil {
			adapted.AppsRaw = make(caddycore.ModuleMap)
		}
		adapted.AppsRaw[appName] = appConfig
	}
	persistConfig := false
	adapted.Admin = &caddycore.AdminConfig{
		Disabled: true,
		Config:   &caddycore.ConfigSettings{Persist: &persistConfig},
	}
	configuration, err = json.Marshal(adapted)
	if err != nil {
		return nil, nil, err
	}
	return configuration, warnings, nil
}

func (runtime *Runtime) Stop() error {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if runtime == nil || processRuntime.active != runtime || !runtime.active {
		contract, err := loadBuildContract()
		if err != nil {
			return err
		}
		return errors.New(contract.Diagnostics.RuntimeNotActive)
	}
	if err := caddycore.Stop(); err != nil {
		return err
	}
	runtime.active = false
	processRuntime.active = nil
	return nil
}

func loadBuildContract() (buildContract, error) {
	contents, err := caddyassets.Contract(caddyassets.CaddyBuild)
	if err != nil {
		return buildContract{}, err
	}
	var contract buildContract
	if err := json.Unmarshal(contents, &contract); err != nil {
		return buildContract{}, err
	}
	return contract, nil
}
