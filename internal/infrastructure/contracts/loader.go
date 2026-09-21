// Package contracts loads static Gateway contract files from the deployed assets bundle.
package contracts

import "os"

func Read(path string) ([]byte, error) {
	return os.ReadFile(path)
}
