package config

import "testing"

func TestValidateYAMLUsesSchemaCompiler(t *testing.T) {
	valid := "registry:\n  path: ./registry\nlisteners: {}\n"
	if err := ValidateYAML(valid); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	if err := ValidateYAML("listeners:\n  web:\n    unknown: true\n"); err == nil {
		t.Fatal("invalid document accepted")
	}
}
