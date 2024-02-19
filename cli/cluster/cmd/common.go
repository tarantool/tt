package cmd

import (
	"errors"
	"fmt"
	"os"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/connect"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

// printRawClusterConfig prints a raw cluster configuration or an instance
// configuration if the instance name is specified.
func printRawClusterConfig(config *libcluster.Config,
	instance string, validate bool) error {
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
func printInstanceConfig(config libcluster.ClusterConfig,
	instance string, full, validate bool) error {
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

// connectOpts is additional connect options specified by a user.
type connectOpts struct {
	Username string
	Password string
}

// connectTarantool establishes a connection to Tarantool.
func connectTarantool(uriOpts UriOpts, connOpts connectOpts) (tarantool.Connector, error) {
	addr, connectorOpts := MakeConnectOptsFromUriOpts(uriOpts)
	if connectorOpts.User == "" && connectorOpts.Pass == "" {
		connectorOpts.User = connOpts.Username
		connectorOpts.Pass = connOpts.Password
		if connectorOpts.User == "" {
			connectorOpts.User = os.Getenv(connect.TarantoolUsernameEnv)
		}
		if connectorOpts.Pass == "" {
			connectorOpts.Pass = os.Getenv(connect.TarantoolPasswordEnv)
		}
	}

	conn, err := tarantool.Connect(addr, connectorOpts)
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

	etcdcli, err := libcluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	return etcdcli, nil
}

// doOnStorage determines a storage based on the opts.
func doOnStorage(connOpts connectOpts, opts UriOpts,
	tarantoolFunc func(tarantool.Connector) error, etcdFunc func(*clientv3.Client) error) error {
	etcdcli, errEtcd := connectEtcd(opts, connOpts)
	if errEtcd == nil {
		return etcdFunc(etcdcli)
	}

	conn, errTarantool := connectTarantool(opts, connOpts)
	if errTarantool == nil {
		return tarantoolFunc(conn)
	}

	return fmt.Errorf("failed to establish a connection to tarantool or etcd: %w, %w",
		errTarantool, errEtcd)
}

// createPublisherAndCollector creates a new data publisher and collector based on UriOpts.
func createPublisherAndCollector(
	publishers libcluster.DataPublisherFactory,
	collectors libcluster.CollectorFactory,
	connOpts connectOpts,
	opts UriOpts) (libcluster.DataPublisher, libcluster.Collector, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Key, opts.Timeout

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

	if err := doOnStorage(connOpts, opts, tarantoolFunc, etcdFunc); err != nil {
		return nil, nil, nil, err
	}

	return publisher, collector, closeFunc, nil
}
