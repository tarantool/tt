package cmd

import (
	"fmt"
	"net/url"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/integrity"
)

// PublishCtx contains information abould cluster publish command execution
// context.
type PublishCtx struct {
	// Username defines a username for connection.
	Username string
	// Password defines a password for connection.
	Password string
	// Force defines whether the publish should be forced and a validation step
	// is omitted.
	Force bool
	// Publishers defines a used data publishers factory.
	Publishers integrity.DataPublisherFactory
	// Collectors defines a used collectors factory.
	Collectors cluster.CollectorFactory
	// Src is a raw data to publish.
	Src []byte
	// Config is a parsed raw data configuration to publish.
	Config *cluster.Config
}

// PublishUri publishes a configuration to URI.
func PublishUri(publishCtx PublishCtx, uri *url.URL) error {
	uriOpts, err := ParseUriOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}

	instance := uriOpts.Instance
	if err := publishCtxValidateConfig(publishCtx, instance); err != nil {
		return err
	}

	connOpts := connectOpts{
		Username: publishCtx.Username,
		Password: publishCtx.Password,
	}
	publisher, collector, cancel, err := createPublisherAndCollector(
		publishCtx.Publishers,
		publishCtx.Collectors,
		connOpts, uriOpts)
	if err != nil {
		return err
	}
	defer cancel()

	if instance == "" {
		// The easy case, just publish the configuration as is.
		return publisher.Publish(0, publishCtx.Src)
	}

	return replaceInstanceConfig(instance, publishCtx.Config, collector, publisher)
}

// PublishCluster publishes a configuration to the configuration path.
func PublishCluster(publishCtx PublishCtx, path, instance string) error {
	if err := publishCtxValidateConfig(publishCtx, instance); err != nil {
		return err
	}

	publisher, err := publishCtx.Publishers.NewFile(path)
	if err != nil {
		return fmt.Errorf("failed to create a file publisher: %w", err)
	}

	if instance == "" {
		// The easy case, just publish the configuration as is.
		return publisher.Publish(0, publishCtx.Src)
	}

	collector, err := publishCtx.Collectors.NewFile(path)
	if err != nil {
		return fmt.Errorf("failed to create a file collector: %w", err)
	}

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
	collector cluster.Collector, publisher integrity.DataPublisher) error {
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
