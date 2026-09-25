package main

import (
	"encoding/json"
	"errors"
	"os"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"gopkg.in/yaml.v3"
)

type fieldContract struct {
	Codes struct {
		UnknownField     string `yaml:"unknownField"`
		BootstrapInvalid string `yaml:"bootstrapInvalid"`
	} `yaml:"codes"`
}

type observation struct {
	Valid  bool   `json:"valid"`
	Code   string `json:"code"`
	Status int    `json:"status"`
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	err := config.Validate(os.Args[1])
	result := observation{Valid: err == nil}
	if err != nil {
		contents, loadErr := assets.Contract(assets.ConfigFields)
		if loadErr != nil {
			os.Exit(2)
		}
		var contract fieldContract
		if loadErr := yaml.Unmarshal(contents, &contract); loadErr != nil {
			os.Exit(2)
		}
		result.Code = contract.Codes.BootstrapInvalid
		if errors.Is(err, config.ErrUnknownField) {
			result.Code = contract.Codes.UnknownField
		}
		catalog, loadErr := config.LoadErrorCatalog()
		if loadErr != nil {
			os.Exit(2)
		}
		problem, found := catalog.Lookup(result.Code)
		if !found {
			os.Exit(2)
		}
		result.Status = problem.Status
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(2)
	}
}
