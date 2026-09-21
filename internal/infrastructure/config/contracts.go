// Package config contains configuration compiler infrastructure adapters.
package config

import "os"

func ReadContract(path string) ([]byte, error) {
	return os.ReadFile(path)
}
