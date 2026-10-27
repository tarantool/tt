package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"

	goconfig "github.com/tarantool/go-config/v2"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
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

// publishFile publishes publishCtx.Src into the file at path, as publish
// does.
func publishFile(publishCtx PublishCtx, path, instance string) error {
	publisher, err := publishCtx.Publishers.NewFilePublisher(path)
	if err != nil {
		return fmt.Errorf("failed to create a file publisher: %w", err)
	}

	return publish(publishCtx, publishCtx.Collectors.NewFileCollector(path), publisher, instance)
}

// PublishApp publishes a configuration into the cluster config of the pair
// of an application, the file at path: into the config storage the file
// points to with config.etcd or config.storage, with the endpoints, the
// prefix, the credentials and the TLS settings it gives, and into the file
// itself when it points to none.
//
// A file that points to a storage is left as it is. Written over with the
// whole config, it would no longer point there: its instances would leave
// the storage for the file at their next start, while the storage kept the
// config they left. The instances the file declares itself next to the
// reference stay in the topology the config is checked with
// (PublishCtx.CheckTopology), as the file wins over the storage.
//
// A file that cannot be read as a cluster config points to no storage
// tarantool could follow either, and the config goes into it, mending it;
// what kept it from being read is told.
func PublishApp(publishCtx PublishCtx, path, instance string) error {
	err := publishCtxValidateConfig(publishCtx, instance)
	if err != nil {
		return err
	}

	stor, found, err := cluster.OpenConfigStorage(context.Background(), path,
		publishCtx.Collectors, publishCtx.Publishers)
	if errors.Is(err, cluster.ErrConfigFile) {
		log.Warnf("%s; the config is published into the file", err)

		return publishFile(publishCtx, path, instance)
	}

	if err != nil {
		return err
	}

	if !found {
		return publishFile(publishCtx, path, instance)
	}

	defer stor.Close()

	check := publishCtx.CheckTopology
	if check != nil {
		publishCtx.CheckTopology = func(topology []string) error {
			return check(append(slices.Clone(topology), stor.FileInstances...))
		}
	}

	return publish(publishCtx, stor.Collector, stor.Publisher, instance)
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
		if errors.Is(err, sdkcluster.ErrNoConfiguration) {
			return fmt.Errorf("failed to get a cluster configuration to update an "+
				"instance %q: %w; there is no cluster config to set the config of the "+
				"instance in: publish the cluster config first", instance, err)
		}

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
