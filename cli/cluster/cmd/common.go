package cmd

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/cli/cluster"
)

// printRawClusterConfig prints a raw cluster configuration or an instance
// configuration if the instance name is specified.
func printRawClusterConfig(config *cluster.Config,
	instance string, validate bool) error {
	cconfig, err := cluster.MakeClusterConfig(config)
	if err != nil {
		return err
	}

	if instance == "" {
		var err error
		if validate {
			err = validateClusterConfig(cconfig, false)
		}
		printConfig(cconfig.RawConfig)
		return err
	}

	return printInstanceConfig(cconfig, instance, false, validate)
}

// printClusterConfig prints a full-merged cluster configuration or an instance
// configuration if the instance name is specified.
func printClusterConfig(cconfig cluster.ClusterConfig,
	instance string, validate bool) error {
	if instance == "" {
		var err error
		if validate {
			err = validateClusterConfig(cconfig, true)
		}
		printConfig(cconfig.RawConfig)
		return err
	}

	return printInstanceConfig(cconfig, instance, true, validate)
}

// printInstanceConfig prints an instance configuration in the cluster.
func printInstanceConfig(config cluster.ClusterConfig,
	instance string, full, validate bool) error {
	if !cluster.HasInstance(config, instance) {
		return fmt.Errorf("instance %q not found", instance)
	}

	var (
		err     error
		iconfig *cluster.Config
	)
	if full {
		ic, _ := cluster.GetInstanceConfig(config, instance)
		iconfig = ic.RawConfig
	} else {
		iconfig = cluster.Instantiate(config, instance)
	}

	if validate {
		err = validateInstanceConfig(iconfig, instance)
	}
	printConfig(iconfig)
	return err
}

// validateRawConfig validates a raw cluster or an instance configuration. The
// configuration belongs to an instance if name != "".
func validateRawConfig(config *cluster.Config, name string) error {
	if name == "" {
		return validateRawClusterConfig(config)
	} else {
		return validateInstanceConfig(config, name)
	}
}

// validateRawClusterConfig validates a raw cluster configuration or an
// instance configuration if the instance name is specified.
func validateRawClusterConfig(config *cluster.Config) error {
	cconfig, err := cluster.MakeClusterConfig(config)
	if err != nil {
		return err
	}

	return validateClusterConfig(cconfig, false)
}

// validateClusterConfig validates a cluster configuration.
func validateClusterConfig(cconfig cluster.ClusterConfig, full bool) error {
	var errs []error
	if err := cluster.Validate(cconfig.RawConfig, cluster.TarantoolSchema); err != nil {
		err = fmt.Errorf("an invalid cluster configuration: %s", err)
		errs = append(errs, err)
	}

	for _, name := range cluster.Instances(cconfig) {
		var iconfig *cluster.Config
		if full {
			ic, err := cluster.GetInstanceConfig(cconfig, name)
			if err != nil {
				return err
			}
			iconfig = ic.RawConfig
		} else {
			iconfig = cluster.Instantiate(cconfig, name)
		}
		if err := validateInstanceConfig(iconfig, name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// validateInstanceConfig validates an instance configuration.
func validateInstanceConfig(config *cluster.Config, name string) error {
	if err := cluster.Validate(config, cluster.TarantoolSchema); err != nil {
		return fmt.Errorf("an invalid instance %q configuration: %w", name, err)
	}
	return nil
}

// printConfig just prints a configuration to stdout.
func printConfig(config *cluster.Config) {
	fmt.Print(config.String())
}
