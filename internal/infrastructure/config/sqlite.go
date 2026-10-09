package config

import (
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type SQLiteContract struct {
	Driver                 string `yaml:"driver"`
	ParentDirectoryMode    uint32 `yaml:"parentDirectoryMode"`
	DatabaseFileMode       uint32 `yaml:"databaseFileMode"`
	MaxOpenConnections     int    `yaml:"maxOpenConnections"`
	MaxIdleConnections     int    `yaml:"maxIdleConnections"`
	SchemaVersion          int    `yaml:"schemaVersion"`
	SystemGroupID          string `yaml:"systemGroupID"`
	HasMigrationTableQuery string `yaml:"hasMigrationTableQuery"`
	MigrationVersionQuery  string `yaml:"migrationVersionQuery"`
	SchemaVersionError     string `yaml:"schemaVersionError"`
	IntegrityCheckQuery    string `yaml:"integrityCheckQuery"`
	ForeignKeyCheckQuery   string `yaml:"foreignKeyCheckQuery"`
	IntegritySuccess       string `yaml:"integritySuccess"`
	IntegrityError         string `yaml:"integrityError"`
	BackupIntoQuery        string `yaml:"backupIntoQuery"`
	Pragmas                string `yaml:"pragmas"`
	Schema                 []byte
}

func LoadSQLiteContract() (SQLiteContract, error) {
	contract := sqliteDefinitions()
	contract.Schema = []byte(storage.SchemaSQL)
	return contract, nil
}
