package config

import (
	"errors"

	assets "github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type PluginInventoryContract struct {
	SelectInstances string   `yaml:"selectInstances"`
	ValidStates     []string `yaml:"validStates"`
	JSON            struct {
		ID       string `yaml:"id"`
		State    string `yaml:"state"`
		Revision string `yaml:"revision"`
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
	if contract.SelectInstances == "" || len(contract.ValidStates) == 0 ||
		contract.JSON.ID == "" || contract.JSON.State == "" || contract.JSON.Revision == "" ||
		contract.Diagnostics.InvalidContract == "" || contract.Diagnostics.InvalidRecord == "" || contract.Diagnostics.InvalidManifest == "" {
		return PluginInventoryContract{}, errors.New(contract.Diagnostics.InvalidContract)
	}
	return contract, nil
}
