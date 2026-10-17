package config

type ServiceInventoryContract struct {
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

func LoadServiceInventoryContract() (ServiceInventoryContract, error) {
	return inventoryDefinitions(), nil
}
