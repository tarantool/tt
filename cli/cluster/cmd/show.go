package cmd

import (
	"fmt"

	"github.com/tarantool/tt/cli/cluster"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
)

// ShowCtx contains information about cluster show command execution context.
type ShowCtx struct {
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Collectors defines a used collectors factory.
	Collectors libcluster.CollectorFactory
	// Validate defines whether the command will check the showed
	// configuration.
	Validate bool
}

// ShowUri shows a configuration from URI.
func ShowUri(showCtx ShowCtx, opts connect.UriOpts) error {
	connOpts := connectOpts{
		Username: showCtx.Username,
		Password: showCtx.Password,
	}
	_, collector, cancel, err := createPublisherAndCollector(
		nil,
		showCtx.Collectors,
		connOpts, opts)
	if err != nil {
		return err
	}
	defer cancel()

	config, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to collect a configuration: %w", err)
	}

	instance := opts.Params["name"]
	if showCtx.Validate {
		if err = validateRawConfig(config, instance); err != nil {
			return err
		}
	}

	return printRawClusterConfig(config, instance, showCtx.Validate)
}

// ShowCluster shows a full cluster configuration for a configuration path.
func ShowCluster(showCtx ShowCtx, path, name string) error {
	config, err := cluster.GetClusterConfig(showCtx.Collectors, path)
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration: %w", err)
	}

	return printClusterConfig(config, name, showCtx.Validate)
}
