package config

import (
	"encoding/json"
	"errors"

	assets "github.com/Liapoldus/core"
)

type PluginRuntimeContract struct {
	Modes struct {
		Local  string `json:"local"`
		Remote string `json:"remote"`
	} `json:"modes"`
	LaunchFields struct {
		Binary string `json:"binary"`
	} `json:"launchFields"`
	Defaults struct {
		CallTimeout            string `json:"callTimeout"`
		StartTimeout           string `json:"startTimeout"`
		MaxConcurrentCalls     int    `json:"maxConcurrentCalls"`
		RestartEnabled         bool   `json:"restartEnabled"`
		RestartInitialBackoff  string `json:"restartInitialBackoff"`
		RestartMaximumBackoff  string `json:"restartMaximumBackoff"`
		HealthProbeInterval    string `json:"healthProbeInterval"`
		HealthFailureThreshold int    `json:"healthFailureThreshold"`
		MemoryProbeInterval    string `json:"memoryProbeInterval"`
		MemoryLimitBytes       uint64 `json:"memoryLimitBytes"`
	} `json:"defaults"`
	ConfigSecrets struct {
		MaximumBytes        int64  `json:"maximumBytes"`
		RequireAbsolutePath bool   `json:"requireAbsolutePath"`
		GrantPurpose        string `json:"grantPurpose"`
	} `json:"configSecrets"`
	Diagnostics struct {
		InvalidContract string `json:"invalidContract"`
		InvalidLaunch   string `json:"invalidLaunch"`
		StartupFailed   string `json:"startupFailed"`
	} `json:"diagnostics"`
}

func LoadPluginRuntimeContract() (PluginRuntimeContract, error) {
	contents, err := assets.Contract(assets.PluginRuntime)
	if err != nil {
		return PluginRuntimeContract{}, err
	}
	var contract PluginRuntimeContract
	if err := json.Unmarshal(contents, &contract); err != nil {
		return PluginRuntimeContract{}, err
	}
	if contract.Modes.Local == "" || contract.Modes.Remote == "" ||
		contract.LaunchFields.Binary == "" ||
		contract.ConfigSecrets.MaximumBytes < 1 || !contract.ConfigSecrets.RequireAbsolutePath || contract.ConfigSecrets.GrantPurpose == "" ||
		contract.Defaults.CallTimeout == "" || contract.Defaults.StartTimeout == "" || contract.Defaults.MaxConcurrentCalls < 1 ||
		contract.Defaults.RestartInitialBackoff == "" || contract.Defaults.RestartMaximumBackoff == "" ||
		contract.Defaults.HealthProbeInterval == "" || contract.Defaults.HealthFailureThreshold < 1 || contract.Defaults.MemoryProbeInterval == "" ||
		contract.Diagnostics.InvalidContract == "" || contract.Diagnostics.InvalidLaunch == "" ||
		contract.Diagnostics.StartupFailed == "" {
		return PluginRuntimeContract{}, errors.New(contract.Diagnostics.InvalidContract)
	}
	return contract, nil
}

func LoadPluginLaunchSchema() ([]byte, error) {
	return assets.Contract(assets.PluginLocalLaunchSchema)
}
