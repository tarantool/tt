package cmd

import (
	"errors"
	"fmt"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/tt/cli/cluster"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

// printRawClusterConfig prints a raw cluster configuration or an instance
// configuration if the instance name is specified.
func printRawClusterConfig(config *libcluster.Config,
	instance string, validate bool,
) error {
	cconfig, err := libcluster.MakeClusterConfig(config)
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
func printClusterConfig(cconfig libcluster.ClusterConfig,
	instance string, validate bool,
) error {
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
func printInstanceConfig(config libcluster.ClusterConfig,
	instance string, full, validate bool,
) error {
	if !libcluster.HasInstance(config, instance) {
		return fmt.Errorf("instance %q not found", instance)
	}

	var (
		err     error
		iconfig *libcluster.Config
	)
	if full {
		ic, _ := cluster.GetInstanceConfig(config, instance)
		iconfig = ic.RawConfig
	} else {
		iconfig = libcluster.Instantiate(config, instance)
	}

	if validate {
		err = validateInstanceConfig(iconfig, instance)
	}
	printConfig(iconfig)
	return err
}

// validateRawConfig validates a raw cluster or an instance configuration. The
// configuration belongs to an instance if name != "".
func validateRawConfig(config *libcluster.Config, name string) error {
	if name == "" {
		return validateRawClusterConfig(config)
	} else {
		return validateInstanceConfig(config, name)
	}
}

// validateRawClusterConfig validates a raw cluster configuration or an
// instance configuration if the instance name is specified.
func validateRawClusterConfig(config *libcluster.Config) error {
	cconfig, err := libcluster.MakeClusterConfig(config)
	if err != nil {
		return err
	}

	return validateClusterConfig(cconfig, false)
}

// validateClusterConfig validates a cluster configuration.
func validateClusterConfig(cconfig libcluster.ClusterConfig, full bool) error {
	var errs []error
	if err := libcluster.Validate(cconfig.RawConfig, libcluster.TarantoolSchema); err != nil {
		err = fmt.Errorf("an invalid cluster configuration: %s", err)
		errs = append(errs, err)
	}

	for _, name := range libcluster.Instances(cconfig) {
		var iconfig *libcluster.Config
		if full {
			ic, err := cluster.GetInstanceConfig(cconfig, name)
			if err != nil {
				return err
			}
			iconfig = ic.RawConfig
		} else {
			iconfig = libcluster.Instantiate(cconfig, name)
		}
		if err := validateInstanceConfig(iconfig, name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// validateInstanceConfig validates an instance configuration.
func validateInstanceConfig(config *libcluster.Config, name string) error {
	if err := libcluster.Validate(config, libcluster.TarantoolSchema); err != nil {
		return fmt.Errorf("an invalid instance %q configuration: %w", name, err)
	}
	return nil
}

// printConfig just prints a configuration to stdout.
func printConfig(config *libcluster.Config) {
	fmt.Print(config.String())
}

// createPublisherAndCollector creates a new data publisher and collector based on UriOpts.
func createPublisherAndCollector(
	publishers libcluster.DataPublisherFactory,
	collectors libcluster.CollectorFactory,
	connOpts libcluster.ConnectOpts,
	opts libconnect.UriOpts,
) (libcluster.DataPublisher, libcluster.Collector, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Params["key"], opts.Timeout

	var (
		publisher libcluster.DataPublisher
		collector libcluster.Collector
		err       error
		closeFunc func()
	)

	tarantoolFunc := func(conn tarantool.Connector) error {
		if publishers != nil {
			publisher, err = publishers.NewTarantool(conn, prefix, key, timeout)
			if err != nil {
				conn.Close()
				return fmt.Errorf("failed to create tarantool config storage publisher: %w", err)
			}
		}
		if collectors != nil {
			collector, err = collectors.NewTarantool(conn, prefix, key, timeout)
			if err != nil {
				conn.Close()
				return fmt.Errorf("failed to create tarantool config storage collector: %w", err)
			}
		}
		closeFunc = func() { conn.Close() }
		return nil
	}

	etcdFunc := func(client *clientv3.Client) error {
		if publishers != nil {
			publisher, err = publishers.NewEtcd(client, prefix, key, timeout)
			if err != nil {
				client.Close()
				return fmt.Errorf("failed to create etcd publisher: %w", err)
			}
		}
		if collectors != nil {
			collector, err = collectors.NewEtcd(client, prefix, key, timeout)
			if err != nil {
				client.Close()
				return fmt.Errorf("failed to create etcd collector: %w", err)
			}
		}
		closeFunc = func() { client.Close() }
		return nil
	}

	if err := libcluster.DoOnStorage(connOpts, opts, tarantoolFunc, etcdFunc); err != nil {
		return nil, nil, nil, err
	}

	return publisher, collector, closeFunc, nil
}
