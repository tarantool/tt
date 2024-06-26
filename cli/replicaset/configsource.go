package replicaset

import (
	"errors"
	"fmt"
	"sort"

	libcluster "github.com/tarantool/tt/lib/cluster"
)

// KeyPicker picks a key to patch.
type KeyPicker func(keys []string, force bool) (int, error)

// CConfigSource describes the cluster config source.
type CConfigSource struct {
	collector libcluster.DataCollector
	publisher DataPublisher
	keyPicker KeyPicker
}

// NewCConfigSource creates CConfigSource.
func NewCConfigSource(collector libcluster.DataCollector, publisher DataPublisher,
	keyPicker KeyPicker) *CConfigSource {
	return &CConfigSource{
		collector: collector,
		publisher: publisher,
		keyPicker: keyPicker,
	}
}

// DataPublisher can publish the config by the specified key, which contains the prefix.
type DataPublisher interface {
	// Publish publishes the config.
	Publish(string, int64, []byte) error
}

// collectConfig fetches and merges the config data.
func collectCConfig(
	collector libcluster.DataCollector) ([]libcluster.Data, libcluster.ClusterConfig, error) {
	var clusterConfig libcluster.ClusterConfig

	configData, err := collector.Collect()
	if err != nil {
		return nil, clusterConfig, fmt.Errorf("failed to collect cluster config: %w", err)
	}
	clusterConfigCollector := libcluster.NewYamlDataMergeCollector(configData...)
	merged, err := clusterConfigCollector.Collect()
	if err != nil {
		return nil, clusterConfig, err
	}
	clusterConfig, err = libcluster.MakeClusterConfig(merged)
	if err != nil {
		return nil, clusterConfig, fmt.Errorf("failed to make cluster config: %w", err)
	}
	return configData, clusterConfig, nil
}

// pickTarget applies keyPicker to the targets slice and returns picked target.
func (c *CConfigSource) pickTarget(targets []patchTarget, force bool) (patchTarget, error) {
	targetKeys := make([]string, 0, len(targets))
	for _, target := range targets {
		targetKeys = append(targetKeys, target.key)
	}
	dstIndex, err := c.keyPicker(targetKeys, force)
	if err != nil {
		return patchTarget{}, err
	}
	return targets[dstIndex], nil
}

// patchInstanceConfig runs an instance based config patching pipeline.
func (c *CConfigSource) patchInstanceConfig(instanceName string, force bool,
	getPathFunc func(cconfigInstance) ([]string, int, error),
	patchFunc func(*libcluster.Config, cconfigInstance) (*libcluster.Config, error),
) error {
	configData, clusterConfig, err := collectCConfig(c.collector)
	if err != nil {
		return err
	}
	inst, err := getCConfigInstance(&clusterConfig, instanceName)
	if err != nil {
		return err
	}

	path, depth, err := getPathFunc(inst)
	if err != nil {
		return err
	}
	targets, err := getCConfigPatchTargets(configData, path, depth)
	if err != nil {
		return err
	}
	target, err := c.pickTarget(targets, force)
	if err != nil {
		return err
	}

	patched, err := patchFunc(target.config, inst)
	if err != nil {
		return err
	}
	err = c.publisher.Publish(target.key, target.revision, []byte(patched.String()))
	if err != nil {
		return fmt.Errorf("failed to publish the config: %w", err)
	}
	return nil
}

// Promote patches a config to promote an instance.
func (c *CConfigSource) Promote(ctx PromoteCtx) error {
	return c.patchInstanceConfig(
		ctx.InstName,
		ctx.Force,
		getCConfigPromotePath,
		func(config *libcluster.Config, inst cconfigInstance) (*libcluster.Config, error) {
			return patchCConfigPromote(config, inst)
		},
	)
}

// Demote patches a config to demote an instance.
func (c *CConfigSource) Demote(ctx DemoteCtx) error {
	return c.patchInstanceConfig(
		ctx.InstName,
		ctx.Force,
		getCConfigDemotePath,
		func(config *libcluster.Config, inst cconfigInstance) (*libcluster.Config, error) {
			return patchCConfigDemote(config, inst)
		},
	)
}

// Expel patches a config to expel an instance.
func (c *CConfigSource) Expel(ctx ExpelCtx) error {
	return c.patchInstanceConfig(
		ctx.InstName,
		ctx.Force,
		getCConfigExpelPath,
		func(config *libcluster.Config, inst cconfigInstance) (*libcluster.Config, error) {
			return patchCConfigExpel(config, inst)
		},
	)
}

// getCConfigPromotePath returns a path and it's minimum interesting depth
// to patch the config for instance promoting.
// For example, if we have the path "/groups/g/replicasets/r/leader" then
// we consider the configs which contains the paths (in the priority order):
// * "/groups/g/replicasets/r/leader"
// * "/groups/g/replicasets/r"
func getCConfigPromotePath(inst cconfigInstance) (path []string, depth int, err error) {
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	switch failover {
	case FailoverOff:
		path = []string{"groups", groupName, "replicasets",
			replicasetName, "instances", instName, "database", "mode"}
		depth = len(path) - 2
	case FailoverManual:
		path = []string{"groups", groupName, "replicasets",
			replicasetName, "leader"}
		depth = len(path) - 1
	case FailoverElection:
		err = fmt.Errorf(`unsupported failover: %q, supported: "manual", "off"`, failover)
	default:
		err = fmt.Errorf(`unknown failover, supported: "manual", "off"`)
	}
	return
}

// getCConfigDemotePath returns a path and it's minimum interesting depth
// to patch the config for instance demoting.
func getCConfigDemotePath(inst cconfigInstance) (path []string, depth int, err error) {
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	switch failover {
	case FailoverOff:
		path = []string{"groups", groupName, "replicasets",
			replicasetName, "instances", instName, "database", "mode"}
		depth = len(path) - 2
	case FailoverManual, FailoverElection:
		err = fmt.Errorf(`unsupported failover: %q, supported: "off"`, failover)
	default:
		err = fmt.Errorf(`unknown failover, supported: "off"`)
	}
	return
}

// getCConfigExpelPath returns a path and it's minimum interesting depth
// to patch the config for instance expelling.
func getCConfigExpelPath(inst cconfigInstance) (path []string, depth int, err error) {
	var (
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	path = []string{"groups", groupName, "replicasets", replicasetName,
		"instances", instName, "iproto", "listen"}
	depth = len(path) - 2
	return
}

// patchTarget describes a cluster config patch target.
type patchTarget struct {
	key      string
	revision int64
	config   *libcluster.Config
	priority int
}

// greater orders patch targets by the priority.
func (target patchTarget) greater(oth patchTarget) bool {
	if target.priority != oth.priority {
		return target.priority > oth.priority
	}
	// If the priorities are equal, lexicographically smaller keys are first.
	return target.key < oth.key
}

// getCConfigPatchTargets extracts patch target from the config data.
// It returns the slice contains targets in the priority order.
func getCConfigPatchTargets(data []libcluster.Data,
	path []string, depth int) ([]patchTarget, error) {
	var targets []patchTarget
	for _, item := range data {
		config, err := libcluster.NewYamlCollector(item.Value).Collect()
		if err != nil {
			return nil,
				fmt.Errorf("failed to decode the config by the key %q: %w", item.Source, err)
		}
		depth, err := getCConfigPathDepth(config, path, depth)
		if err != nil {
			return nil, err
		}
		if depth != noDepth {
			targets = append(targets, patchTarget{
				key:      item.Source,
				revision: item.Revision,
				config:   config,
				priority: depth,
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].greater(targets[j])
	})
	return targets, nil
}

const noDepth = -1

// getCConfigPathDepth returns the maximum depth of the path contained in the config.
// If it is less than lowerDepth, it returns noDepth.
func getCConfigPathDepth(config *libcluster.Config,
	path []string, lowerDepth int) (int, error) {
	for i := len(path); i >= lowerDepth; i-- {
		_, err := config.Get(path[:i])
		var notExistErr libcluster.NotExistError
		if errors.As(err, &notExistErr) {
			continue
		}
		if err != nil {
			return noDepth, err
		}
		return i, nil
	}
	return noDepth, nil
}
