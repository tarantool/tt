package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/go-tlsdialer"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
)

const (
	defaultEtcdTimeout = 3 * time.Second
)

var (
	mainEnvCollector = libcluster.NewEnvCollector(func(path []string) string {
		middle := strings.ToUpper(strings.Join(path, "_"))
		return fmt.Sprintf("TT_%s", middle)
	})
	defaultEnvCollector = libcluster.NewEnvCollector(func(path []string) string {
		middle := strings.ToUpper(strings.Join(path, "_"))
		return fmt.Sprintf("TT_%s_DEFAULT", middle)
	})
)

// collectEtcdConfig collects a configuration from etcd with options from
// the cluster configuration.
func collectEtcdConfig(collectors libcluster.CollectorFactory,
	clusterConfig libcluster.ClusterConfig) (*libcluster.Config, error) {
	etcdConfig := clusterConfig.Config.Etcd
	opts := libcluster.EtcdOpts{
		Endpoints: etcdConfig.Endpoints,
		Username:  etcdConfig.Username,
		Password:  etcdConfig.Password,
		KeyFile:   etcdConfig.Ssl.KeyFile,
		CertFile:  etcdConfig.Ssl.CertFile,
		CaPath:    etcdConfig.Ssl.CaPath,
		CaFile:    etcdConfig.Ssl.CaFile,
	}
	if !etcdConfig.Ssl.VerifyPeer || !etcdConfig.Ssl.VerifyHost {
		opts.SkipHostVerify = true
	}
	if etcdConfig.Http.Request.Timeout != 0 {
		var err error
		timeout := fmt.Sprintf("%fs", etcdConfig.Http.Request.Timeout)
		opts.Timeout, err = time.ParseDuration(timeout)
		if err != nil {
			fmtErr := "unable to parse a etcd request timeout: %w"
			return nil, fmt.Errorf(fmtErr, err)
		}
	} else {
		opts.Timeout = defaultEtcdTimeout
	}

	etcd, err := libcluster.ConnectEtcd(opts)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to etcd: %w", err)
	}
	defer etcd.Close()

	etcdCollector, err := collectors.NewEtcd(etcd, etcdConfig.Prefix, "", opts.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd collector: %w", err)
	}

	etcdRawConfig, err := etcdCollector.Collect()
	if err != nil {
		return nil, fmt.Errorf("unable to get config from etcd: %w", err)
	}
	return etcdRawConfig, nil
}

// collectTarantoolConfig collects a configuration from tarantool config
// storage with options from the tarantool configuration.
func collectTarantoolConfig(collectors libcluster.CollectorFactory,
	clusterConfig libcluster.ClusterConfig) (*libcluster.Config, error) {
	type tarantoolOpts struct {
		addr   string
		dialer tarantool.Dialer
		opts   tarantool.Opts
	}

	tarantoolConfig := clusterConfig.Config.Storage
	var opts []tarantoolOpts
	for _, endpoint := range tarantoolConfig.Endpoints {
		var network, address string
		if !connect.IsBaseURI(endpoint.Uri) {
			network = "tcp"
			address = endpoint.Uri
		} else {
			network, address = connect.ParseBaseURI(endpoint.Uri)
		}
		addr := fmt.Sprintf("%s://%s", network, address)
		if endpoint.Params.Transport == "" || endpoint.Params.Transport != "ssl" {
			opts = append(opts, tarantoolOpts{
				addr: addr,
				dialer: tarantool.NetDialer{
					Address:  addr,
					User:     endpoint.Login,
					Password: endpoint.Password,
				},
				opts: tarantool.Opts{
					SkipSchema: true,
				},
			})
		} else {
			opts = append(opts, tarantoolOpts{
				addr: addr,
				dialer: tlsdialer.OpenSSLDialer{
					Address:     addr,
					User:        endpoint.Login,
					Password:    endpoint.Password,
					SslKeyFile:  endpoint.Params.SslKeyFile,
					SslCertFile: endpoint.Params.SslCertFile,
					SslCaFile:   endpoint.Params.SslCaFile,
					SslCiphers:  endpoint.Params.SslCiphers,
				},
				opts: tarantool.Opts{
					SkipSchema: true,
				},
			})
		}
	}

	var connectionErrors []error
	cconfig := libcluster.NewConfig()
	for _, opt := range opts {
		ctx := context.Background()
		if opt.opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opt.opts.Timeout)
			defer cancel()
		}

		conn, err := tarantool.Connect(ctx, opt.dialer, opt.opts)
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("error when connecting to endpoint %q: %w", opt.addr, err))
			continue
		} else {
			defer conn.Close()

			tarantoolCollector, err := collectors.NewTarantool(conn,
				tarantoolConfig.Prefix, "",
				time.Duration(tarantoolConfig.Timeout*float64(time.Second)))
			if err != nil {
				connectionErrors = append(connectionErrors,
					fmt.Errorf("error when creating a collector for endpoint %q: %w",
						opt.addr, err))
				continue
			}

			config, err := tarantoolCollector.Collect()
			if err != nil {
				connectionErrors = append(connectionErrors,
					fmt.Errorf("error when collecting config from endpoint %q: %w", opt.addr, err))
				continue
			}

			cconfig.Merge(config)
		}
	}

	return cconfig, errors.Join(connectionErrors...)
}

// GetClusterConfig returns a cluster configuration loaded from a path to
// a config file. It uses a a config file, etcd and default environment
// variables as sources. The function returns a cluster config as is, without
// merging of settings from scopes: global, group, replicaset, instance.
func GetClusterConfig(collectors libcluster.CollectorFactory,
	path string) (libcluster.ClusterConfig, error) {
	ret := libcluster.ClusterConfig{}
	if path == "" {
		return ret, fmt.Errorf("a configuration file must be set")
	}

	config := libcluster.NewConfig()

	mainEnvConfig, err := mainEnvCollector.Collect()
	if err != nil {
		fmtErr := "failed to collect a config from environment variables: %w"
		return ret, fmt.Errorf(fmtErr, err)
	}
	config.Merge(mainEnvConfig)

	collector, err := collectors.NewFile(path)
	if err != nil {
		return ret, fmt.Errorf("failed to create a file collector: %w", err)
	}

	fileConfig, err := collector.Collect()
	if err != nil {
		fmtErr := "unable to get cluster config from %q: %w"
		return ret, fmt.Errorf(fmtErr, path, err)
	}
	config.Merge(fileConfig)

	clusterConfig, err := libcluster.MakeClusterConfig(config)
	if err != nil {
		return ret, fmt.Errorf("unable to parse cluster config from file: %w", err)
	}
	if len(clusterConfig.Config.Etcd.Endpoints) > 0 {
		etcdConfig, err := collectEtcdConfig(collectors, clusterConfig)
		if err != nil {
			return ret, err
		}
		config.Merge(etcdConfig)
	}

	if len(clusterConfig.Config.Storage.Endpoints) > 0 {
		tarantoolConfig, err := collectTarantoolConfig(collectors, clusterConfig)
		if err != nil {
			return ret, err
		}
		config.Merge(tarantoolConfig)
	}

	defaultEnvConfig, err := defaultEnvCollector.Collect()
	if err != nil {
		fmtErr := "failed to collect a config from default environment variables: %w"
		return ret, fmt.Errorf(fmtErr, err)
	}

	config.Merge(defaultEnvConfig)
	return libcluster.MakeClusterConfig(config)
}

// GetInstanceConfig returns a full configuration for an instance with the
// name from a cluster config. It merges the configuration from all configured
// sources and scopes: environment, global, group, replicaset, instance.
func GetInstanceConfig(cluster libcluster.ClusterConfig,
	instance string) (libcluster.InstanceConfig, error) {
	if !libcluster.HasInstance(cluster, instance) {
		return libcluster.InstanceConfig{},
			fmt.Errorf("an instance %q not found", instance)
	}

	mainEnvConfig, err := mainEnvCollector.Collect()
	if err != nil {
		fmtErr := "failed to collect a config from environment variables: %w"
		return libcluster.InstanceConfig{}, fmt.Errorf(fmtErr, err)
	}

	iconfig := libcluster.NewConfig()
	iconfig.Merge(mainEnvConfig)
	iconfig.Merge(libcluster.Instantiate(cluster, instance))

	return libcluster.MakeInstanceConfig(iconfig)
}
