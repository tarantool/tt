package cmd

import (
	"fmt"

	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
)

// PublishCtx contains information about cluster publish command execution
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
	Publishers libcluster.DataPublisherFactory
	// Collectors defines a used collectors factory.
	Collectors libcluster.CollectorFactory
	// Src is a raw data to publish.
	Src []byte
	// Config is a parsed raw data configuration to publish.
	Config *libcluster.Config
	// Group is a group name for a new instance configuration publishing.
	Group string
	// Replicaset is a replicaset name for a new instance configuration publishing.
	Replicaset string
}

// PublishUri publishes a configuration to URI.
func PublishUri(publishCtx PublishCtx, opts connect.UriOpts) error {
	instance := opts.Params["name"]
	if err := publishCtxValidateConfig(publishCtx, instance); err != nil {
		return err
	}

	connOpts := libcluster.ConnectOpts{
		Username: publishCtx.Username,
		Password: publishCtx.Password,
	}
	publisher, collector, cancel, err := createPublisherAndCollector(
		publishCtx.Publishers,
		publishCtx.Collectors,
		connOpts, opts)
	if err != nil {
		return err
	}
	defer cancel()

	if instance == "" {
		// The easy case, just publish the configuration as is.
		return publisher.Publish(0, publishCtx.Src)
	}

	return setInstanceConfig(publishCtx.Group, publishCtx.Replicaset, instance,
		publishCtx.Config, collector, publisher)
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

	return setInstanceConfig(publishCtx.Group, publishCtx.Replicaset, instance,
		publishCtx.Config, collector, publisher)
}

// publishCtxValidateConfig validates a source configuration from the publish
// context.
func publishCtxValidateConfig(publishCtx PublishCtx, instance string) error {
	if !publishCtx.Force {
		return validateRawConfig(publishCtx.Config, instance)
	}
	return nil
}

// setInstanceConfig sets an instance configuration in the collected
// cluster configuration and republishes it.
func setInstanceConfig(group, replicaset, instance string, config *libcluster.Config,
	collector libcluster.Collector, publisher libcluster.DataPublisher,
) error {
	src, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration to update "+
			"an instance %q: %w", instance, err)
	}

	cconfig, err := libcluster.MakeClusterConfig(src)
	if err != nil {
		return fmt.Errorf("failed to parse a target configuration: %w", err)
	}

	gname, rname, found := libcluster.FindInstance(cconfig, instance)
	if found {
		// Instance is present in the configuration.
		if replicaset != "" && replicaset != rname {
			return fmt.Errorf("wrong replicaset name, expected %q, have %q", rname, replicaset)
		}
		if group != "" && group != gname {
			return fmt.Errorf("wrong group name, expected %q, have %q", gname, group)
		}
		cconfig, err = libcluster.ReplaceInstanceConfig(cconfig, instance, config)
		if err != nil {
			return fmt.Errorf("failed to replace an instance %q configuration "+
				"in a cluster configuration: %w", instance, err)
		}
		return libcluster.NewYamlConfigPublisher(publisher).Publish(cconfig.RawConfig)
	}

	if replicaset == "" {
		return fmt.Errorf(
			"replicaset name is not specified for %q instance configuration", instance)
	}
	if group == "" {
		// Try to determine a group.
		var found bool
		group, found = libcluster.FindGroupByReplicaset(cconfig, replicaset)
		if !found {
			return fmt.Errorf("failed to determine the group of the %q replicaset", replicaset)
		}
	}
	cconfig, err = libcluster.SetInstanceConfig(cconfig, group, replicaset,
		instance, config)
	if err != nil {
		return fmt.Errorf("failed to set an instance %q configuration: %w", instance, err)
	}

	return libcluster.NewYamlConfigPublisher(publisher).Publish(cconfig.RawConfig)
}
