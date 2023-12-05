package cmd

import (
	"fmt"
	"net/url"

	"github.com/tarantool/tt/cli/cluster"
)

// ShowCtx contains information about cluster show command execution context.
type ShowCtx struct {
	// Username defines an etcd username.
	Username string
	// Password defines an etcd password.
	Password string
	// Validate defines whether the command will check the showed
	// configuration.
	Validate bool
}

// ShowEtcd shows a configuration from etcd.
func ShowEtcd(showCtx ShowCtx, uri *url.URL) error {
	etcdOpts, err := cluster.MakeEtcdOptsFromUrl(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}
	if etcdOpts.Username == "" && etcdOpts.Password == "" {
		etcdOpts.Username = showCtx.Username
		etcdOpts.Password = showCtx.Password
	}

	etcdcli, err := cluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}
	defer etcdcli.Close()

	prefix, key, timeout := etcdOpts.Prefix, etcdOpts.Key, etcdOpts.Timeout
	var collector cluster.Collector
	if key == "" {
		collector = cluster.NewEtcdAllCollector(etcdcli, prefix, timeout)
	} else {
		collector = cluster.NewEtcdKeyCollector(etcdcli, prefix, key, timeout)
	}

	config, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to collect a configuration from etcd: %w", err)
	}

	name := uri.Query().Get("name")
	if showCtx.Validate {
		err = validateRawConfig(config, name)
	}

	return printRawClusterConfig(config, uri.Query().Get("name"), showCtx.Validate)
}

// ShowCluster shows a full cluster configuration for a configuration path.
func ShowCluster(showCtx ShowCtx, path, name string) error {
	config, err := cluster.GetClusterConfig(path)
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration: %w", err)
	}

	return printClusterConfig(config, name, showCtx.Validate)
}
