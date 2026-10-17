package config

import (
	"encoding/json"
	"errors"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrInvalidDocument = errors.New("invalid core document")

// ValidateJSONSchemaDocument applies a service-owned JSON Schema to an opaque
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
