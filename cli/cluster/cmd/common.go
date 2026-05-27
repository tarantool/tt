package cmd

import (
	"context"
	"errors"
	"fmt"

	goconfig "github.com/tarantool/go-config"

	"github.com/tarantool/tt/cli/cluster"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

// printGoConfig prints a goconfig.Config to stdout as YAML.
func printGoConfig(cfg goconfig.Config) error {
	b, err := cfg.MarshalYAML()
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	fmt.Print(string(b))
	return nil
}

// printRawClusterConfig prints a raw cluster configuration or an instance
// configuration if the instance name is specified. yamlBytes is the raw YAML
// content of the config file or storage key.
func printRawClusterConfig(yamlBytes []byte,
	instance string, validate bool,
) error {
	view, err := cluster.BuildGoConfigFromBytes(context.Background(), yamlBytes)
	if err != nil {
		return fmt.Errorf("failed to build config view: %w", err)
	}

	if instance == "" {
		var validateErr error
		if validate {
			validateErr = validateGoConfig(view, false)
		}
		if printErr := printGoConfig(view); printErr != nil {
			return printErr
		}
		return validateErr
	}

	return printInstanceConfig(view, instance, false, validate)
}

// printInstanceConfig prints an instance configuration in the cluster.
// goView is a goconfig.Config (with inheritance) for the full cluster config.
// The instance effective (inheritance-resolved) config is always printed.
func printInstanceConfig(goView goconfig.Config,
	instance string, _ bool, validate bool,
) error {
	instView, err := cluster.InstanceConfig(goView, instance)
	if err != nil {
		return fmt.Errorf("instance %q not found", instance)
	}

	var validateErr error
	if validate {
		validateErr = validateInstanceConfig(goView, instance)
	}
	if printErr := printGoConfig(instView); printErr != nil {
		return printErr
	}
	return validateErr
}

// validateRawConfig validates a raw cluster or an instance configuration.
// yamlBytes is the raw YAML content; name is the instance name (empty means
// validate the whole cluster config).
func validateRawConfig(yamlBytes []byte, name string) error {
	if name == "" {
		return validateRawClusterConfig(yamlBytes)
	}
	view, err := cluster.BuildGoConfigFromBytes(context.Background(), yamlBytes)
	if err != nil {
		return fmt.Errorf("failed to build config for validation: %w", err)
	}
	return validateInstanceConfig(view, name)
}

// validateRawClusterConfig validates a raw cluster configuration.
func validateRawClusterConfig(yamlBytes []byte) error {
	view, err := cluster.BuildGoConfigFromBytes(context.Background(), yamlBytes)
	if err != nil {
		return fmt.Errorf("failed to build config for validation: %w", err)
	}
	return validateGoConfig(view, false)
}

// validateGoConfig validates a goconfig.Config as a cluster configuration.
// Each discovered instance is validated using its effective (inherited) config
// (obtained via cluster.InstanceConfig which calls EffectiveAll internally).
// The full parameter is unused for the raw-bytes path but kept for symmetry.
func validateGoConfig(view goconfig.Config, _ bool) error {
	var errs []error
	if err := cluster.Validate(view); err != nil {
		errs = append(errs, fmt.Errorf("an invalid cluster configuration: %s", err))
	}

	names, err := cluster.Instances(view)
	if err != nil {
		return fmt.Errorf("failed to list instances: %w", err)
	}

	for _, name := range names {
		instView, err := cluster.InstanceConfig(view, name)
		if err != nil {
			return err
		}
		if err := validateInstanceConfig(instView, name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// validateInstanceConfig validates an instance configuration.
// instCfg is the already-resolved (effective) goconfig.Config for that instance.
// name is used only in the error message.
func validateInstanceConfig(instCfg goconfig.Config, name string) error {
	if err := cluster.Validate(instCfg); err != nil {
		return fmt.Errorf("an invalid instance %q configuration: %w", name, err)
	}
	return nil
}

// openRemoteCollector dials the remote storage described by opts and binds a
// collector-flavored *RawStorage using the given factory's integrity options.
// The returned cleanup releases the connection.
func openRemoteCollector(factory libcluster.Factory,
	connOpts libcluster.ConnectOpts, opts libconnect.UriOpts,
) (libcluster.DataCollector, func(), error) {
	stor, cleanup, storageType, err := libcluster.NewStorageConnection(connOpts, opts)
	if err != nil {
		return nil, nil, err
	}
	collector, err := factory.NewRemoteStorage(stor, opts.Prefix,
		opts.Params["key"], opts.Timeout, storageType)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create %s collector: %w", storageType, err)
	}
	return collector, func() { cleanup() }, nil
}

// openCollectorAndPublisher dials the remote storage described by opts and
// binds two *RawStorage instances on the same connection — one for collecting
// (using collectors' integrity verifiers) and one for publishing (using
// publishers' integrity signer/verifiers).
func openCollectorAndPublisher(
	collectors, publishers libcluster.Factory,
	connOpts libcluster.ConnectOpts,
	opts libconnect.UriOpts,
) (libcluster.DataCollector, libcluster.DataPublisher, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Params["key"], opts.Timeout

	stor, cleanup, storageType, err := libcluster.NewStorageConnection(connOpts, opts)
	if err != nil {
		return nil, nil, nil, err
	}

	publisher, err := publishers.NewRemoteStorage(stor, prefix, key, timeout, storageType)
	if err != nil {
		cleanup()
		return nil, nil, nil,
			fmt.Errorf("failed to create %s publisher: %w", storageType, err)
	}

	collector, err := collectors.NewRemoteStorage(stor, prefix, key, timeout, storageType)
	if err != nil {
		cleanup()
		return nil, nil, nil,
			fmt.Errorf("failed to create %s collector: %w", storageType, err)
	}

	return collector, publisher, func() { cleanup() }, nil
}
