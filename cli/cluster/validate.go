package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/go-config/v2/collectors"
	gcttarantool "github.com/tarantool/go-config/v2/tarantool"
	"github.com/tarantool/go-config/v2/validators/jsonschema"
)

var (
	errNoTarantoolSchemasRegistered = errors.New("no Tarantool schemas registered")
	errNoReplicasetSchema           = errors.New("the schema has no replicaset properties")
)

// Validate validates a goconfig.Config as a cluster configuration against the
// embedded JSON Schema for the newest known Tarantool version. Every key is
// checked on the level it stands on: "leader" and "bootstrap_leader" are
// accepted in a replicaset only, as Tarantool accepts them, and are refused
// globally, in a group and in an instance.
func Validate(cfg goconfig.Config) error {
	return validate(cfg, newestSchema)
}

// ValidateInstance validates a goconfig.Config as the configuration of one
// instance, the one sdk.InstanceConfig resolves: the global section, the
// group, the replicaset and the instance folded into one.
//
// The folded config holds what only a replicaset may set, "leader" and
// "bootstrap_leader", next to what any level may, so it is checked against
// the schema of the cluster config with those keys of the replicaset added.
// On which level a key stands is not left in the folded config: [Validate]
// of the cluster config checks that, and a key on a wrong level is refused
// there. A key unknown to Tarantool and a value of a wrong type are refused
// here as well.
func ValidateInstance(cfg goconfig.Config) error {
	return validate(cfg, newestInstanceSchema)
}

// validate validates cfg against the schema that schema returns.
func validate(cfg goconfig.Config, schema func() ([]byte, error)) error {
	yamlBytes, err := cfg.MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if len(bytes.TrimSpace(yamlBytes)) == 0 {
		return nil
	}

	schemaBytes, err := schema()
	if err != nil {
		return err
	}

	validator, err := jsonschema.New(schemaBytes)
	if err != nil {
		return fmt.Errorf("failed to compile schema: %w", err)
	}

	node, err := collectors.NewYamlFormat().From(bytes.NewReader(yamlBytes)).Parse()
	if err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	valErrs := validator.Validate(node)
	if len(valErrs) == 0 {
		return nil
	}

	errs := make([]error, len(valErrs))
	for i := range valErrs {
		errs[i] = &valErrs[i]
	}

	return errors.Join(errs...)
}

// newestSchema returns the JSON Schema of the cluster config for the newest
// Tarantool version known.
func newestSchema() ([]byte, error) {
	versions := gcttarantool.SchemaVersions()
	if len(versions) == 0 {
		return nil, errNoTarantoolSchemasRegistered
	}

	newest := versions[len(versions)-1]

	schemaBytes, err := gcttarantool.Schema(newest)
	if err != nil {
		return nil, fmt.Errorf("schema for Tarantool version %q not found: %w", newest, err)
	}

	return schemaBytes, nil
}

// newestInstanceSchema returns the JSON Schema of the cluster config for the
// newest Tarantool version known with the keys only a replicaset may set,
// everything of its schema but its instances, added to the top level: the
// schema of an instance config folded from all levels.
func newestInstanceSchema() ([]byte, error) {
	schemaBytes, err := newestSchema()
	if err != nil {
		return nil, err
	}

	var schema map[string]any

	err = json.Unmarshal(schemaBytes, &schema)
	if err != nil {
		return nil, fmt.Errorf("failed to parse schema: %w", err)
	}

	top, _ := schema["properties"].(map[string]any)
	replicaset := nested(schema, "properties", "groups", "additionalProperties",
		"properties", "replicasets", "additionalProperties", "properties")

	if top == nil || replicaset == nil {
		return nil, errNoReplicasetSchema
	}

	for key, value := range replicaset {
		_, onTop := top[key]
		if !onTop && key != "instances" {
			top[key] = value
		}
	}

	schemaBytes, err = json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to build the instance schema: %w", err)
	}

	return schemaBytes, nil
}

// nested returns the object at the path of keys in obj, nil if there is none.
func nested(obj map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		next, ok := obj[key].(map[string]any)
		if !ok {
			return nil
		}

		obj = next
	}

	return obj
}
