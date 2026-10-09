package config

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrInvalidDocument = errors.New("invalid core document")

// ReadSecretReference resolves the narrow file: reference used by the private
// SDK control plane. It is not a Core bootstrap parser and never exposes the
// reference outside the one authenticated redemption call.
func ReadSecretReference(bootstrapPath, reference string, maximumBytes int64) ([]byte, error) {
	const prefix = "file:"
	if bootstrapPath == "" || maximumBytes <= 0 || len(reference) <= len(prefix) || reference[:len(prefix)] != prefix {
		return nil, ErrInvalidDocument
	}
	path := reference[len(prefix):]
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(bootstrapPath), path)
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, ErrInvalidDocument
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumBytes {
		return nil, ErrInvalidDocument
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || int64(len(contents)) > maximumBytes {
		clear(contents)
		return nil, ErrInvalidDocument
	}
	return contents, nil
}

// ValidateJSONSchemaDocument applies a plugin-owned JSON Schema to an opaque
// JSON document without decoding it into a Core product model.
func ValidateJSONSchemaDocument(document, schemaContents []byte) error {
	var instance any
	if !json.Valid(document) || json.Unmarshal(document, &instance) != nil {
		return ErrInvalidDocument
	}
	return validateJSONSchemaValue(instance, schemaContents)
}

func validateJSONSchemaValue(instance any, contents []byte) error {
	var schemaDocument struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(contents, &schemaDocument); err != nil {
		return err
	}
	var schemaValue any
	if err := json.Unmarshal(contents, &schemaValue); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaDocument.ID, schemaValue); err != nil {
		return err
	}
	schema, err := compiler.Compile(schemaDocument.ID)
	if err != nil {
		return err
	}
	return schema.Validate(instance)
}
