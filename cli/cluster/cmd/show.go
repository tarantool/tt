package cmd

import (
	"context"
	"fmt"

	goconfig "github.com/tarantool/go-config"
	"github.com/tarantool/tt/cli/cluster"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/integrity"
)

// ShowCtx contains information about cluster show command execution context.
type ShowCtx struct {
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Collectors defines a used data collectors factory for URI-based show.
	Collectors libcluster.Factory
	// Integrity holds the integrity context used for file-based show.
	Integrity integrity.IntegrityCtx
	// Validate defines whether the command will check the showed
	// configuration.
	Validate bool
}

// ShowUri shows a configuration from URI.
func ShowUri(showCtx ShowCtx, opts connect.UriOpts) error {
	connOpts := libcluster.ConnectOpts{
		Username: showCtx.Username,
		Password: showCtx.Password,
	}
	collector, cancel, err := openRemoteCollector(showCtx.Collectors, connOpts, opts)
	if err != nil {
		return err
	}
	defer cancel()

	yamlBytes, err := cluster.CollectDataBytes(context.Background(), collector)
	if err != nil {
		return fmt.Errorf("failed to collect a configuration: %w", err)
	}

	instance := opts.Params["name"]
	if showCtx.Validate {
		if err = validateRawConfig(yamlBytes, instance); err != nil {
			return err
		}
	}

	return printRawClusterConfig(yamlBytes, instance, showCtx.Validate)
}

// ShowCluster shows a full cluster configuration for a configuration path.
func ShowCluster(showCtx ShowCtx, path, name string) error {
	ctx := context.Background()
	config, err := cluster.GetClusterConfig(ctx, path, showCtx.Integrity)
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration: %w", err)
	}

	return printClusterConfig(config, name, showCtx.Validate)
}

// printClusterConfig prints a full-merged cluster configuration or an instance
// configuration if the instance name is specified.
func printClusterConfig(cconfig *goconfig.MutableConfig,
	instance string, validate bool,
) error {
	snap := cconfig.Snapshot()
	if instance == "" {
		var validateErr error
		if validate {
			validateErr = validateGoConfig(snap, true)
		}
		if printErr := printGoConfig(snap); printErr != nil {
			return printErr
		}
		return validateErr
	}

	return printInstanceConfig(snap, instance, true, validate)
}
