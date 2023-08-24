package cmd

import (
	"fmt"
	"net/url"

	"github.com/tarantool/tt/cli/cluster"
)

// PublishCtx contains information abould cluster publish command execution
// context.
type PublishCtx struct {
	// Force defines whether the publish should be forced and a validation step
	// is omitted.
	Force bool
	// Src is a raw data to publish.
	Src []byte
	// Config is a parsed raw data configuration to publish.
	Config *cluster.Config
}

// PublishEtcd publishes a configuration to etcd.
func PublishEtcd(publishCtx PublishCtx, uri *url.URL) error {
	etcdOpts, err := cluster.MakeEtcdOptsFromUrl(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}

	instance := uri.Query().Get("name")
	if err := publishCtxValidateConfig(publishCtx, instance); err != nil {
		return err
	}

	etcdcli, err := cluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}
	defer etcdcli.Close()

	prefix, timeout := etcdOpts.Prefix, etcdOpts.Timeout
	publisher := cluster.NewEtcdDataPublisher(etcdcli, prefix, timeout)
	if instance == "" {
		// The easy case, just publish the configuration as is.
		return publisher.Publish(publishCtx.Src)
	}

	collector := cluster.NewEtcdCollector(etcdcli, prefix, timeout)
	return replaceInstanceConfig(instance, publishCtx.Config, collector, publisher)
}

// PublishCluster publishes a configuration to the configuration path.
func PublishCluster(publishCtx PublishCtx, path, instance string) error {
	if err := publishCtxValidateConfig(publishCtx, instance); err != nil {
		return err
	}

	publisher := cluster.NewFileDataPublisher(path)
	if instance == "" {
		// The easy case, just publish the configuration as is.
		return publisher.Publish(publishCtx.Src)
	}

	collector := cluster.NewFileCollector(path)
	return replaceInstanceConfig(instance, publishCtx.Config, collector, publisher)
}

// publishCtxValidateConfig validates a source configuration from the publish
// context.
func publishCtxValidateConfig(publishCtx PublishCtx, instance string) error {
	if !publishCtx.Force {
		return validateRawConfig(publishCtx.Config, instance)
	}
	return nil
}

// replaceInstanceConfig replaces an instance configuration in the collected
// cluster configuration and republishes it.
func replaceInstanceConfig(instance string, config *cluster.Config,
	collector cluster.Collector, publisher cluster.DataPublisher) error {
	src, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration to update "+
			"an instance %q: %w", instance, err)
	}

	cconfig, err := cluster.MakeClusterConfig(src)
	if err != nil {
		return fmt.Errorf("failed to parse a target configuration: %w", err)
	}

	cconfig, err = cluster.ReplaceInstanceConfig(cconfig, instance, config)
	if err != nil {
		return fmt.Errorf("failed to replace an instance %q configuration "+
			"in a cluster configuration: %w", instance, err)
	}

	return cluster.NewYamlConfigPublisher(publisher).Publish(cconfig.RawConfig)
}
