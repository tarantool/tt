package cmd

import (
	"context"
	"fmt"

	goconfig "github.com/tarantool/go-config"

	"github.com/tarantool/tt/cli/cluster"
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
	Publishers libcluster.Factory
	// Collectors defines a used data collectors factory.
	Collectors libcluster.Factory
	// Src is raw YAML data to publish.
	Src []byte
	// Config is the decoded payload from Src (map[string]any from YAML
	// unmarshal). Used by the per-instance publish path.
	Config map[string]any
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
	collector, publisher, cancel, err := openCollectorAndPublisher(
		publishCtx.Collectors,
		publishCtx.Publishers,
		connOpts, opts)
	if err != nil {
		return err
	}
	defer cancel()

	if instance == "" {
		// The easy case, just publish the configuration as is.
		return publisher.Publish(0, publishCtx.Src)
	}

	// Build a MutableConfig from the target (collected) key's bytes.
	collectedBytes, err := cluster.CollectDataBytes(context.Background(), collector)
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration to update an instance %q: %w",
			instance, err)
	}
	mut, err := cluster.BuildMutableFromBytes(context.Background(), collectedBytes)
	if err != nil {
		return fmt.Errorf("failed to build mutable config: %w", err)
	}

	return setInstanceConfig(publishCtx.Group, publishCtx.Replicaset, instance,
		publishCtx.Config, mut, publisher)
}

// PublishCluster publishes a configuration to the configuration path.
func PublishCluster(publishCtx PublishCtx, path, instance string) error {
	if err := publishCtxValidateConfig(publishCtx, instance); err != nil {
		return err
	}

	publisher, err := publishCtx.Publishers.NewFilePublisher(path)
	if err != nil {
		return fmt.Errorf("failed to create a file publisher: %w", err)
	}

	if instance == "" {
		// The easy case, just publish the configuration as is.
		return publisher.Publish(0, publishCtx.Src)
	}

	collector := publishCtx.Collectors.NewFileCollector(path)
	collectedBytes, err := cluster.CollectDataBytes(context.Background(), collector)
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration to update an instance %q: %w",
			instance, err)
	}
	mut, err := cluster.BuildMutableFromBytes(context.Background(), collectedBytes)
	if err != nil {
		return fmt.Errorf("failed to build mutable config: %w", err)
	}

	return setInstanceConfig(publishCtx.Group, publishCtx.Replicaset, instance,
		publishCtx.Config, mut, publisher)
}

// publishCtxValidateConfig validates a source configuration from the publish
// context.
func publishCtxValidateConfig(publishCtx PublishCtx, instance string) error {
	if !publishCtx.Force {
		return validateRawConfig(publishCtx.Src, instance)
	}
	return nil
}

// setInstanceConfig sets an instance configuration in the collected cluster
// configuration and republishes it.
//
// mut is a MutableConfig built from the target key's current bytes. The
// function locates the instance, validates group/replicaset names, patches the
// instance subtree, marshals the result, and publishes it.
func setInstanceConfig(group, replicaset, instance string, instanceMap map[string]any,
	mut *goconfig.MutableConfig, publisher libcluster.DataPublisher,
) error {
	snap := mut.Snapshot()

	gname, rname, found := cluster.FindInstance(snap, instance)
	if found {
		// Instance already exists: validate group/replicaset names.
		if replicaset != "" && replicaset != rname {
			return fmt.Errorf("wrong replicaset name, expected %q, have %q", rname, replicaset)
		}
		if group != "" && group != gname {
			return fmt.Errorf("wrong group name, expected %q, have %q", gname, group)
		}
		group = gname
		replicaset = rname
	} else {
		// Instance not found: resolve group/replicaset.
		if replicaset == "" {
			return fmt.Errorf(
				"replicaset name is not specified for %q instance configuration", instance)
		}
		if group == "" {
			var ok bool
			group, ok = cluster.FindGroupByReplicaset(snap, replicaset)
			if !ok {
				return fmt.Errorf("failed to determine the group of the %q replicaset", replicaset)
			}
		}
	}

	keyPath := goconfig.NewKeyPath(
		fmt.Sprintf("groups/%s/replicasets/%s/instances/%s", group, replicaset, instance))
	if err := mut.Set(keyPath, instanceMap); err != nil {
		return fmt.Errorf("failed to set instance %q configuration: %w", instance, err)
	}

	afterSet := mut.Snapshot()
	b, err := afterSet.MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal cluster config: %w", err)
	}

	return publisher.Publish(0, b)
}
