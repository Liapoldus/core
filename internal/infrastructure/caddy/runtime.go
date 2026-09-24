package caddy

import (
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
	active bool
}

var processRuntime = struct {
	sync.Mutex
	active *Runtime
}{}

func StartCaddyfile(source []byte) (*Runtime, []caddyconfig.Warning, error) {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if processRuntime.active != nil {
		contract, err := loadBuildContract()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New(contract.Diagnostics.RuntimeAlreadyActive)
	}

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
	if err := caddycore.Load(configuration, true); err != nil {
		return nil, nil, err
	}
	runtime := &Runtime{active: true}
	processRuntime.active = runtime
	return runtime, warnings, nil
}

func (runtime *Runtime) Stop() error {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if processRuntime.active != runtime {
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
