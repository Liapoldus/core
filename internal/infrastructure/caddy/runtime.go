package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	caddyassets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/models"
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
	active         bool
	plugins        []PluginInstance
	source         []byte
	adminDirectory string
	adminSocket    string
	adminClient    *AdminClient
	adminHTTP      *http.Client
	adminOptions   *AdminRuntimeOptions
}

var processRuntime = struct {
	sync.Mutex
	active *Runtime
}{}

func StartCaddyfile(source []byte) (*Runtime, []caddyconfig.Warning, error) {
	return startCaddyfile(source, nil, nil)
}

func StartCaddyfileWithPlugins(source []byte, instances []PluginInstance) (*Runtime, []caddyconfig.Warning, error) {
	return startCaddyfile(source, instances, nil)
}

func StartCaddyfileWithAdmin(source []byte, options AdminRuntimeOptions) (*Runtime, []caddyconfig.Warning, error) {
	return startCaddyfile(source, nil, &options)
}

func StartCaddyfileWithPluginsAndAdmin(source []byte, instances []PluginInstance, options AdminRuntimeOptions) (*Runtime, []caddyconfig.Warning, error) {
	return startCaddyfile(source, instances, &options)
}

func startCaddyfile(source []byte, instances []PluginInstance, adminOptions *AdminRuntimeOptions) (*Runtime, []caddyconfig.Warning, error) {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if processRuntime.active != nil {
		contract, err := loadBuildContract()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New(contract.Diagnostics.RuntimeAlreadyActive)
	}

	adminDirectory := ""
	adminSocket := ""
	adminListen := ""
	var err error
	if adminOptions != nil {
		if err := validateAdminRuntimeOptions(*adminOptions); err != nil {
			return nil, nil, err
		}
		adminDirectory, err = os.MkdirTemp(os.TempDir(), adminOptions.SocketDirectoryPrefix)
		if err != nil {
			return nil, nil, err
		}
		if err := os.Chmod(adminDirectory, os.FileMode(adminOptions.DirectoryMode)); err != nil {
			_ = os.RemoveAll(adminDirectory)
			return nil, nil, err
		}
		adminSocket = filepath.Join(adminDirectory, adminOptions.SocketName)
		adminListen = adminOptions.UnixPrefix + adminSocket
	}
	configuration, warnings, err := adaptCaddyfileWithAdmin(source, instances, adminListen)
	if err != nil {
		if adminDirectory != "" {
			_ = os.RemoveAll(adminDirectory)
		}
		return nil, nil, err
	}
	if err := caddycore.Load(configuration, true); err != nil {
		if adminDirectory != "" {
			_ = os.RemoveAll(adminDirectory)
		}
		return nil, nil, err
	}
	if adminOptions == nil {
		runtime := &Runtime{active: true, plugins: append([]PluginInstance(nil), instances...), source: append([]byte(nil), source...)}
		processRuntime.active = runtime
		return runtime, warnings, nil
	}
	if err := os.Chmod(adminSocket, os.FileMode(adminOptions.SocketMode)); err != nil {
		_ = caddycore.Stop()
		_ = os.RemoveAll(adminDirectory)
		return nil, nil, err
	}
	adminClient, adminHTTP, err := NewUnixAdminClient(adminSocket, *adminOptions)
	if err != nil {
		_ = caddycore.Stop()
		_ = os.RemoveAll(adminDirectory)
		return nil, nil, err
	}
	runtime := &Runtime{
		active: true, plugins: append([]PluginInstance(nil), instances...), source: append([]byte(nil), source...),
		adminDirectory: adminDirectory, adminSocket: adminSocket, adminClient: adminClient,
		adminHTTP: adminHTTP, adminOptions: adminOptions,
	}
	processRuntime.active = runtime
	return runtime, warnings, nil
}

func (runtime *Runtime) ReplaceCaddyfile(source []byte) ([]caddyconfig.Warning, error) {
	return runtime.replaceCaddyfile(source, nil, false)
}

func (runtime *Runtime) ReplaceCaddyfileWithPlugins(source []byte, instances []PluginInstance) error {
	_, err := runtime.replaceCaddyfile(source, instances, true)
	return err
}

func (runtime *Runtime) replaceCaddyfile(source []byte, instances []PluginInstance, replacePlugins bool) ([]caddyconfig.Warning, error) {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if runtime == nil || processRuntime.active != runtime || !runtime.active {
		contract, err := loadBuildContract()
		if err != nil {
			return nil, err
		}
		return nil, errors.New(contract.Diagnostics.RuntimeNotActive)
	}

	instancesForSnapshot := runtime.plugins
	if replacePlugins {
		instancesForSnapshot = instances
	}
	configuration, warnings, err := adaptCaddyfileWithAdmin(source, instancesForSnapshot, runtime.adminListen())
	if err != nil {
		return nil, err
	}
	if err := caddycore.Load(configuration, true); err != nil {
		return nil, err
	}
	if replacePlugins {
		runtime.plugins = append([]PluginInstance(nil), instances...)
	}
	runtime.source = append([]byte(nil), source...)
	return warnings, nil
}

func (runtime *Runtime) Validate(_ context.Context, source []byte) error {
	processRuntime.Lock()
	defer processRuntime.Unlock()
	if runtime == nil || processRuntime.active != runtime || !runtime.active {
		contract, err := loadBuildContract()
		if err != nil {
			return err
		}
		return errors.New(contract.Diagnostics.RuntimeNotActive)
	}
	configuration, _, err := adaptCaddyfileWithAdmin(source, runtime.plugins, runtime.adminListen())
	if err != nil {
		return err
	}
	var adapted caddycore.Config
	if err := json.Unmarshal(configuration, &adapted); err != nil {
		return err
	}
	return caddycore.Validate(&adapted)
}

func (runtime *Runtime) Activate(_ context.Context, source []byte) error {
	_, err := runtime.ReplaceCaddyfile(source)
	return err
}

func AdaptCaddyfile(source []byte) ([]byte, []caddyconfig.Warning, error) {
	return adaptCaddyfile(source, nil)
}

func ValidateCaddyfileWithPlugins(source []byte, instances []PluginInstance) error {
	configuration, _, err := adaptCaddyfile(source, instances)
	if err != nil {
		return err
	}
	var adapted caddycore.Config
	if err := json.Unmarshal(configuration, &adapted); err != nil {
		return err
	}
	return caddycore.Validate(&adapted)
}

func ReplaceCaddyfileWithPlugins(runtime any, source []byte, instances []PluginInstance) error {
	active, ok := runtime.(*Runtime)
	if !ok {
		contract, err := loadBuildContract()
		if err != nil {
			return err
		}
		return errors.New(contract.Diagnostics.RuntimeNotActive)
	}
	return active.ReplaceCaddyfileWithPlugins(source, instances)
}

func adaptCaddyfile(source []byte, instances []PluginInstance) ([]byte, []caddyconfig.Warning, error) {
	return adaptCaddyfileWithAdmin(source, instances, "")
}

func adaptCaddyfileWithAdmin(source []byte, instances []PluginInstance, adminListen string) ([]byte, []caddyconfig.Warning, error) {
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
		Disabled: adminListen == "",
		Listen:   adminListen,
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
	if runtime.adminHTTP != nil {
		if transport, ok := runtime.adminHTTP.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}
	runtime.active = false
	processRuntime.active = nil
	if runtime.adminDirectory != "" {
		return os.RemoveAll(runtime.adminDirectory)
	}
	return nil
}

func (runtime *Runtime) Snapshot(ctx context.Context) ([]byte, error) {
	if runtime == nil || runtime.adminClient == nil {
		return nil, errors.New(runtime.notActive())
	}
	return runtime.adminClient.Snapshot(ctx)
}

func (runtime *Runtime) Request(ctx context.Context, request models.CaddyAdminRequest) (models.CaddyAdminResponse, error) {
	if runtime == nil || runtime.adminClient == nil {
		return models.CaddyAdminResponse{}, errors.New(runtime.notActive())
	}
	return runtime.adminClient.Request(ctx, request)
}

func (runtime *Runtime) notActive() string {
	if runtime == nil {
		contract, err := loadBuildContract()
		if err != nil {
			return ""
		}
		return contract.Diagnostics.RuntimeNotActive
	}
	contract, err := loadBuildContract()
	if err != nil {
		return ""
	}
	return contract.Diagnostics.RuntimeNotActive
}

func (runtime *Runtime) adminListen() string {
	if runtime == nil || runtime.adminOptions == nil {
		return ""
	}
	return runtime.adminOptions.UnixPrefix + runtime.adminSocket
}

func validateAdminRuntimeOptions(options AdminRuntimeOptions) error {
	if options.Client.SnapshotPath == "" || options.Client.PathPrefix == "" || options.Client.RequestBodyBytes < 1 || options.Client.SnapshotBytes < 1 || options.Client.ResponseBodyBytes < 1 || options.Client.RequestTimeout <= 0 || options.Client.InvalidConfiguration == "" || options.Client.AdminUnavailable == "" || options.Client.SnapshotUnavailable == "" || options.UnixPrefix == "" || options.UnixNetwork == "" || options.URLScheme == "" || options.URLHost == "" || options.SocketDirectoryPrefix == "" || options.SocketName == "" || options.DirectoryMode == 0 || options.SocketMode == 0 {
		return errors.New(options.Client.InvalidConfiguration)
	}
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
