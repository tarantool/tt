package cmd

import (
	"context"
	"errors"
	"fmt"

	goconfig "github.com/tarantool/go-config/v2"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/v3/cli/cluster"
)

var (
	errFailedToDetermineTheGroupOfTheReplicaset = errors.New(
		"failed to determine the group of the ",
	)
	errReplicasetNameIsNotSpecifiedForInstanceConfiguration = errors.New(
		"replicaset name is not specified for ",
	)
	errWrongGroupNameExpectedHave      = errors.New("wrong group name, expected ")
	errWrongReplicasetNameExpectedHave = errors.New(
		"wrong replicaset name, expected ",
	)
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
	Publishers sdkcluster.Factory
	// Collectors defines a used data collectors factory.
	Collectors sdkcluster.Factory
	// Src is raw YAML data to publish.
	Src []byte
	// Config is the decoded payload from Src (map[string]any from YAML
	// unmarshal). Used by the per-instance publish path.
	Config map[string]any
	// Group is a group name for a new instance configuration publishing.
	Group string
	// Replicaset is a replicaset name for a new instance configuration publishing.
	Replicaset string
	// CheckTopology, when set, checks the names of the instances of the
	// cluster config about to be published, whole, before it is written: the
	// pair of an application refuses a config that leaves out an instance of
	// its description. nil checks nothing, as for a config storage given by
	// its URI.
	CheckTopology func(topology []string) error
}

// PublishURI publishes a configuration to URI.
func PublishURI(publishCtx PublishCtx, opts connect.URIOpts) error {
	instance := opts.Params["name"]

	err := publishCtxValidateConfig(publishCtx, instance)
	if err != nil {
		return err
	}

	connOpts := sdkcluster.ConnectOpts{
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

	return publish(publishCtx, collector, publisher, instance)
}

// PublishCluster publishes a configuration to the configuration path.
func PublishCluster(publishCtx PublishCtx, path, instance string) error {
	err := publishCtxValidateConfig(publishCtx, instance)
	if err != nil {
		return err
	}

	publisher, err := publishCtx.Publishers.NewFilePublisher(path)
	if err != nil {
		return fmt.Errorf("failed to create a file publisher: %w", err)
	}

	return publish(publishCtx, publishCtx.Collectors.NewFileCollector(path), publisher, instance)
}

// publish publishes publishCtx.Src through publisher: whole, or, for an
// instance, as the config of the instance within the cluster config
// collector collects. What is published is checked first
// (PublishCtx.CheckTopology).
func publish(publishCtx PublishCtx, collector sdkcluster.DataCollector,
	publisher sdkcluster.DataPublisher, instance string,
) error {
	data := publishCtx.Src

	if instance != "" {
		// Build a MutableConfig from the target (collected) key's bytes.
		collectedBytes, err := cluster.CollectDataBytes(context.Background(), collector)
		if err != nil {
			return fmt.Errorf("failed to get a cluster configuration to update an "+
				"instance %q: %w", instance, err)
		}

		mut, err := cluster.BuildMutableFromBytes(context.Background(), collectedBytes)
		if err != nil {
			return fmt.Errorf("failed to build mutable config: %w", err)
		}

		data, err = setInstanceConfig(publishCtx.Group, publishCtx.Replicaset, instance,
			publishCtx.Config, mut)
		if err != nil {
			return err
		}
	}

	err := checkTopology(publishCtx.CheckTopology, data)
	if err != nil {
		return err
	}

	return publisher.Publish(0, data)
}

// checkTopology passes the names of the instances of data, a cluster config
// about to be published, to check, unless check is nil. A config that cannot
// be read is not published then, --force or not: whether it keeps the
// instances it must is unknown.
func checkTopology(check func([]string) error, data []byte) error {
	if check == nil {
		return nil
	}

	cfg, err := cluster.BuildGoConfigFromBytes(context.Background(), data)
	if err != nil {
		return fmt.Errorf("cannot read the cluster config to publish: %w", err)
	}

	topology, err := cluster.Instances(cfg)
	if err != nil {
		return fmt.Errorf("cannot read the topology of the cluster config to publish: %w",
			err)
	}

	return check(topology)
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
// configuration and returns the result to publish.
//
// mut is a MutableConfig built from the target key's current bytes. The
// function locates the instance, validates group/replicaset names, patches the
// instance subtree, and marshals the result.
func setInstanceConfig(group, replicaset, instance string, instanceMap map[string]any,
	mut *goconfig.MutableConfig,
) ([]byte, error) {
	snap := mut.Snapshot()

	gname, rname, found := cluster.FindInstance(snap, instance)
	if !found {
		// Instance not found: resolve group/replicaset.
		if replicaset == "" {
			return nil, fmt.Errorf("%w%q instance configuration",
				errReplicasetNameIsNotSpecifiedForInstanceConfiguration, instance)
		}

		if group == "" {
			var ok bool

			group, ok = cluster.FindGroupByReplicaset(snap, replicaset)

			if !ok {
				return nil, fmt.Errorf("%w%q replicaset",
					errFailedToDetermineTheGroupOfTheReplicaset, replicaset)
			}
		}
	}

	if found {
		// Instance already exists: validate group/replicaset names.
		if replicaset != "" && replicaset != rname {
			return nil, fmt.Errorf("%w%q, have %q",
				errWrongReplicasetNameExpectedHave, rname, replicaset)
		}

		if group != "" && group != gname {
			return nil, fmt.Errorf("%w%q, have %q", errWrongGroupNameExpectedHave, gname, group)
		}

		group = gname
		replicaset = rname
	}

	keyPath := goconfig.NewKeyPath(
		fmt.Sprintf("groups/%s/replicasets/%s/instances/%s", group, replicaset, instance))

	err := mut.Set(keyPath, instanceMap)
	if err != nil {
		return nil, fmt.Errorf("failed to set instance %q configuration: %w", instance, err)
	}

	afterSet := mut.Snapshot()

	b, err := afterSet.MarshalYAML()
	if err != nil {
		return nil, fmt.Errorf("marshal cluster config: %w", err)
	}

	return b, nil
}
