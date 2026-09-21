package models

// Secret is a resolved credential. Its value is tagged so it is never
// serialized to JSON or text, guaranteeing it cannot leak through output.
type Secret struct {
	Value string `json:"-"`
}