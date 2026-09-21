// Package assets exposes immutable Gateway contract assets bundled with a binary.
package assets

import _ "embed"

//go:embed contracts/config-fields.yaml
var configFields []byte

func ConfigFields() []byte {
	return append([]byte(nil), configFields...)
}
