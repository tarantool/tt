package cmd

import (
	"fmt"
	"net/url"

	"github.com/tarantool/tt/cli/cluster"
)

// ShowCtx contains information about cluster show command execution context.
type ShowCtx struct {
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Collectors defines a used collectors factory.
	Collectors cluster.CollectorFactory
	// Validate defines whether the command will check the showed
	// configuration.
	Validate bool
}

// ShowUri shows a configuration from URI.
func ShowUri(showCtx ShowCtx, uri *url.URL) error {
	uriOpts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}

	connOpts := connectOpts{
		Username: showCtx.Username,
		Password: showCtx.Password,
	}
	_, collector, cancel, err := createPublisherAndCollector(
		nil,
		showCtx.Collectors,
		connOpts, uriOpts)
	if err != nil {
		return err
	}
	defer cancel()

	config, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to collect a configuration: %w", err)
	}

	instance := uriOpts.Instance
	if showCtx.Validate {
		err = validateRawConfig(config, instance)
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
