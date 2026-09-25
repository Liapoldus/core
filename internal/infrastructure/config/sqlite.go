package config

import (
	assets "github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type SQLiteContract struct {
	Driver                 string `yaml:"driver"`
	ParentDirectoryMode    uint32 `yaml:"parentDirectoryMode"`
	DatabaseFileMode       uint32 `yaml:"databaseFileMode"`
	MaxOpenConnections     int    `yaml:"maxOpenConnections"`
	MaxIdleConnections     int    `yaml:"maxIdleConnections"`
	SchemaVersion          int    `yaml:"schemaVersion"`
	SystemGroupID          string `yaml:"systemGroupID"`
	ArtifactsDirectoryMode uint32 `yaml:"artifactsDirectoryMode"`
	HasMigrationTableQuery string `yaml:"hasMigrationTableQuery"`
	MigrationVersionQuery  string `yaml:"migrationVersionQuery"`
	SchemaVersionError     string `yaml:"schemaVersionError"`
	Pragmas                string `yaml:"pragmas"`
	Schema                 []byte
}

func LoadSQLiteContract() (SQLiteContract, error) {
	runtimeContents, err := assets.Contract(assets.SQLiteRuntime)
	if err != nil {
		return SQLiteContract{}, err
	}
	var contract SQLiteContract
	if err := yaml.Unmarshal(runtimeContents, &contract); err != nil {
		return SQLiteContract{}, err
	}
	contract.Schema, err = assets.Contract(assets.SQLiteSchema)
	if err != nil {
		return SQLiteContract{}, err
	}
	return contract, nil
}
