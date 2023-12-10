package cmd

import (
	"errors"
	"fmt"
	"os"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
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

// connectOpts is additional connect options specified by a user.
type connectOpts struct {
	Username string
	Password string
}

// connectTarantool establishes a connection to Tarantool.
func connectTarantool(uriOpts UriOpts, connOpts connectOpts) (connector.Connector, error) {
	connectorOpts := MakeConnectOptsFromUriOpts(uriOpts)
	if connectorOpts.Username == "" && connectorOpts.Password == "" {
		connectorOpts.Username = connOpts.Username
		connectorOpts.Password = connOpts.Password
		if connectorOpts.Username == "" {
			connOpts.Username = os.Getenv(connect.TarantoolUsernameEnv)
		}
		if connectorOpts.Password == "" {
			connOpts.Password = os.Getenv(connect.TarantoolPasswordEnv)
		}
	}

	conn, err := connector.Connect(connectorOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tarantool: %w", err)
	}
	return conn, nil
}

// connectEtcd establishes a connection to etcd.
func connectEtcd(uriOpts UriOpts, connOpts connectOpts) (*clientv3.Client, error) {
	etcdOpts := MakeEtcdOptsFromUriOpts(uriOpts)
	if etcdOpts.Username == "" && etcdOpts.Password == "" {
		etcdOpts.Username = connOpts.Username
		etcdOpts.Password = connOpts.Password
		if etcdOpts.Username == "" {
			etcdOpts.Username = os.Getenv(connect.EtcdUsernameEnv)
		}
		if etcdOpts.Password == "" {
			etcdOpts.Password = os.Getenv(connect.EtcdPasswordEnv)
		}
	}

	etcdcli, err := cluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	return etcdcli, nil
}

// createPublisher creates a new data publisher and collector based on UriOpts.
func createPublisherAndCollector(connOpts connectOpts,
	opts UriOpts) (cluster.DataPublisher, cluster.Collector, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Key, opts.Timeout

	conn, errTarantool := connectTarantool(opts, connOpts)
	if errTarantool == nil {
		var (
			publisher cluster.DataPublisher
			collector cluster.Collector
		)
		if key == "" {
			publisher = cluster.NewTarantoolAllDataPublisher(conn, prefix, timeout)
			collector = cluster.NewTarantoolAllCollector(conn, prefix, timeout)
		} else {
			publisher = cluster.NewTarantoolKeyDataPublisher(conn, prefix, key, timeout)
			collector = cluster.NewTarantoolKeyCollector(conn, prefix, key, timeout)
		}
		return publisher, collector, func() { conn.Close() }, nil
	}

	etcdcli, errEtcd := connectEtcd(opts, connOpts)
	if errEtcd == nil {
		var (
			publisher cluster.DataPublisher
			collector cluster.Collector
		)
		if key == "" {
			publisher = cluster.NewEtcdAllDataPublisher(etcdcli, prefix, timeout)
			collector = cluster.NewEtcdAllCollector(etcdcli, prefix, timeout)
		} else {
			publisher = cluster.NewEtcdKeyDataPublisher(etcdcli, prefix, key, timeout)
			collector = cluster.NewEtcdKeyCollector(etcdcli, prefix, key, timeout)
		}
		return publisher, collector, func() { etcdcli.Close() }, nil
	}

	return nil, nil, nil,
		fmt.Errorf("failed to establish a connection to tarantool or etcd: %w, %w",
			errTarantool, errEtcd)
}
