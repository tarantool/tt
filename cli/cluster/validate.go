package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

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
	return validate(cfg, func(compiled schemas) compiledSchema { return compiled.cluster })
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
	return validate(cfg, func(compiled schemas) compiledSchema { return compiled.instance })
}

// compiledSchema is a schema compiled into its validator, or the error that
// kept it from compiling.
type compiledSchema struct {
	validator *jsonschema.Validator
	err       error
}

// schemas are the two schemas Validate and ValidateInstance validate
// against, compiled.
type schemas struct {
	// cluster is the schema of a cluster config (Validate).
	cluster compiledSchema
	// instance is the schema of the config of one instance
	// (ValidateInstance).
	instance compiledSchema
}

// compiledSchemas compiles both schemas once for the run of tt. A schema is
// close to a megabyte and takes tens of milliseconds to compile, and tt
// instance start validates the cluster config and then the config of every
// instance it starts: compiled for each config, they took a tenth of a
// second for an instance and more for each one more. Both are compiled at
// once, as the instance schema follows the cluster one whenever a cluster
// config passes.
var compiledSchemas = sync.OnceValue(func() schemas {
	var compiled schemas

	var compiling sync.WaitGroup

	compiling.Go(func() {
		compiled.instance = compileSchema(newestInstanceSchema)
	})

	compiled.cluster = compileSchema(newestSchema)

	compiling.Wait()

	return compiled
})

// compileSchema compiles the schema that schema returns.
func compileSchema(schema func() ([]byte, error)) compiledSchema {
	schemaBytes, err := schema()
	if err != nil {
		return compiledSchema{validator: nil, err: err}
	}

	validator, err := jsonschema.New(schemaBytes)
	if err != nil {
		return compiledSchema{validator: nil, err: fmt.Errorf("failed to compile schema: %w", err)}
	}

	return compiledSchema{validator: validator, err: nil}
}

// validate validates cfg against the schema of compiledSchemas that pick
// picks.
func validate(cfg goconfig.Config, pick func(schemas) compiledSchema) error {
	yamlBytes, err := cfg.MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if len(bytes.TrimSpace(yamlBytes)) == 0 {
		return nil
	}

	schema := pick(compiledSchemas())
	if schema.err != nil {
		return schema.err
	}

	validator := schema.validator

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
		_, inTop := top[key]
		if !inTop && key != "instances" {
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
