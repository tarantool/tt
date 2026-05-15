package cluster

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/tarantool/go-config/collectors"
	gcttarantool "github.com/tarantool/go-config/tarantool"
	"github.com/tarantool/go-config/validators/jsonschema"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

// Validate validates a configuration against the embedded JSON Schema for the
// newest known Tarantool version.
func Validate(config *libcluster.Config) error {
	yamlBytes := []byte(config.String())
	if len(bytes.TrimSpace(yamlBytes)) == 0 {
		return nil
	}

	versions := gcttarantool.SchemaVersions()
	if len(versions) == 0 {
		return fmt.Errorf("no Tarantool schemas registered")
	}
	newest := versions[len(versions)-1]
	schemaBytes, ok := gcttarantool.Schema(newest)
	if !ok {
		return fmt.Errorf("schema for Tarantool version %q not found", newest)
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
