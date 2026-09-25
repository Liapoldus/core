package config

import (
	"errors"

	assets "github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type PluginInventoryContract struct {
	SelectInstances string   `yaml:"selectInstances"`
	ValidModes      []string `yaml:"validModes"`
	ValidStates     []string `yaml:"validStates"`
	JSON            struct {
		ID                    string `yaml:"id"`
		Mode                  string `yaml:"mode"`
		State                 string `yaml:"state"`
		Revision              string `yaml:"revision"`
		Capabilities          string `yaml:"capabilities"`
		CapabilityDescriptors string `yaml:"capabilityDescriptors"`
		DescriptorCapability  string `yaml:"descriptorCapability"`
		DescriptorModes       string `yaml:"descriptorModes"`
	} `yaml:"json"`
	Diagnostics struct {
		InvalidContract string `yaml:"invalidContract"`
		InvalidRecord   string `yaml:"invalidRecord"`
		InvalidManifest string `yaml:"invalidManifest"`
	} `yaml:"diagnostics"`
}

func LoadPluginInventoryContract() (PluginInventoryContract, error) {
	contents, err := assets.Contract(assets.SQLitePluginInstances)
	if err != nil {
		return PluginInventoryContract{}, err
	}
	var contract PluginInventoryContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return PluginInventoryContract{}, err
	}
	if contract.SelectInstances == "" || len(contract.ValidModes) == 0 || len(contract.ValidStates) == 0 ||
		contract.JSON.ID == "" || contract.JSON.Mode == "" || contract.JSON.State == "" || contract.JSON.Revision == "" ||
		contract.JSON.Capabilities == "" || contract.JSON.CapabilityDescriptors == "" || contract.JSON.DescriptorCapability == "" || contract.JSON.DescriptorModes == "" ||
		contract.Diagnostics.InvalidContract == "" || contract.Diagnostics.InvalidRecord == "" || contract.Diagnostics.InvalidManifest == "" {
		return PluginInventoryContract{}, errors.New(contract.Diagnostics.InvalidContract)
	}
	return contract, nil
}
