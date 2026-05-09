package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gsconnect "github.com/tarantool/go-storage/connect"
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
	clusterConfig libcluster.ClusterConfig,
) (*libcluster.Config, error) {
	var timeout time.Duration
	var err error

	etcdConfig := clusterConfig.Config.Etcd

	switch etcdConfig.Http.Request.Timeout {
	case 0:
		timeout = defaultEtcdTimeout
	default:
		timeoutBase := fmt.Sprintf("%fs", etcdConfig.Http.Request.Timeout)
		timeout, err = time.ParseDuration(timeoutBase)
		if err != nil {
			return nil, fmt.Errorf("unable to parse a etcd request timeout: %w", err)
		}
	}

	ctx := context.Background()

	etcd, cleanup, err := gsconnect.NewEtcdStorage(ctx, gsconnect.Config{
		Endpoints:   etcdConfig.Endpoints,
		Username:    etcdConfig.Username,
		Password:    etcdConfig.Password,
		DialTimeout: timeout,
		SSL: gsconnect.SSLConfig{
			KeyFile:    etcdConfig.Ssl.KeyFile,
			CertFile:   etcdConfig.Ssl.CertFile,
			CaPath:     etcdConfig.Ssl.CaPath,
			CaFile:     etcdConfig.Ssl.CaFile,
			VerifyPeer: etcdConfig.Ssl.VerifyPeer,
			VerifyHost: etcdConfig.Ssl.VerifyHost,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to connect to etcd: %w", err)
	}

	defer cleanup()

	etcdCollector, err := collectors.NewRemoteStorage(etcd, etcdConfig.Prefix, "", timeout, "etcd")
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
	clusterConfig libcluster.ClusterConfig,
) (*libcluster.Config, error) {
	tarantoolConfig := clusterConfig.Config.Storage

	timeout := time.Duration(tarantoolConfig.Timeout * float64(time.Second))

	var connectionErrors []error
	cconfig := libcluster.NewConfig()
	for _, endpoint := range tarantoolConfig.Endpoints {
		var network, address string
		if !connect.IsBaseURI(endpoint.Uri) {
			network = "tcp"
			address = endpoint.Uri
		} else {
			network, address = connect.ParseBaseURI(endpoint.Uri)
		}
		addr := fmt.Sprintf("%s://%s", network, address)

		sslEnable := false
		switch endpoint.Params.Transport {
		case "ssl":
			sslEnable = true
		case "plain":
			sslEnable = false
		case "":
			sslEnable = endpoint.Params.SslKeyFile != "" ||
				endpoint.Params.SslCertFile != "" ||
				endpoint.Params.SslCaFile != "" ||
				endpoint.Params.SslCiphers != "" ||
				endpoint.Params.SslPassword != "" ||
				endpoint.Params.SslPasswordFile != ""
		default:
			connectionErrors = append(connectionErrors,
				fmt.Errorf("error when connecting to endpoint %q: unknown transport type: %s",
					addr, endpoint.Params.Transport))
			continue
		}

		ctx := context.Background()
		stor, cleanup, err := gsconnect.NewTCSStorage(ctx, gsconnect.Config{
			Endpoints:   []string{addr},
			Username:    endpoint.Login,
			Password:    endpoint.Password,
			DialTimeout: timeout,
			SSL: gsconnect.SSLConfig{
				Enable:       sslEnable,
				KeyFile:      endpoint.Params.SslKeyFile,
				CertFile:     endpoint.Params.SslCertFile,
				CaFile:       endpoint.Params.SslCaFile,
				Ciphers:      endpoint.Params.SslCiphers,
				Password:     endpoint.Params.SslPassword,
				PasswordFile: endpoint.Params.SslPasswordFile,
				VerifyPeer:   sslEnable,
				VerifyHost:   sslEnable,
			},
		})
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("error when connecting to endpoint %q: %w", addr, err))
			continue
		}

		tarantoolCollector, err := collectors.NewRemoteStorage(
			stor, tarantoolConfig.Prefix, "", timeout, "tarantool",
		)
		if err != nil {
			cleanup()
			connectionErrors = append(connectionErrors,
				fmt.Errorf("error when creating a collector for endpoint %q: %w", addr, err))
			continue
		}

		config, err := tarantoolCollector.Collect()
		cleanup()
		if err != nil {
			connectionErrors = append(connectionErrors,
				fmt.Errorf("error when collecting config from endpoint %q: %w", addr, err))
			continue
		}

		cconfig.Merge(config)
	}

	return cconfig, errors.Join(connectionErrors...)
}

// GetClusterConfig returns a cluster configuration loaded from a path to
// a config file. It uses a a config file, etcd and default environment
// variables as sources. The function returns a cluster config as is, without
// merging of settings from scopes: global, group, replicaset, instance.
func GetClusterConfig(collectors libcluster.CollectorFactory,
	path string,
) (libcluster.ClusterConfig, error) {
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
	instance string,
) (libcluster.InstanceConfig, error) {
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
