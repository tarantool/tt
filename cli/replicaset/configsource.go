package replicaset

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	libcluster "github.com/tarantool/tt/lib/cluster"
)

// KeyPicker picks a key to patch.
type KeyPicker func(keys []string, force bool, pathMsg string) (int, error)

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

// path describes a path of target with its depth level in config.
type path struct {
	path  []string
	depth int
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
func (c *CConfigSource) pickTarget(targets []patchTarget, force bool,
	pathMsg string) (patchTarget, error) {
	targetKeys := make([]string, 0, len(targets))
	for _, target := range targets {
		targetKeys = append(targetKeys, target.key)
	}
	dstIndex, err := c.keyPicker(targetKeys, force, pathMsg)
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
	target, err := c.pickTarget(targets, force, strings.Join(path, "/"))
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

// patchConfigWithRoles runs an config patching pipeline with adding roles.
func (c *CConfigSource) patchConfigWithRoles(ctx RolesChangeCtx,
	getPathFunc func(clusterConfig libcluster.ClusterConfig,
		ctx RolesChangeCtx) (paths []path, err error),
	updateRolesFunc func([]string, string) ([]string, error),
	patchFunc func(config *libcluster.Config, prt []patchRoleTarget) (*libcluster.Config, error),
) error {
	configData, clusterConfig, err := collectCConfig(c.collector)
	if err != nil {
		return err
	}
	paths, err := getPathFunc(clusterConfig, ctx)
	if err != nil {
		return err
	}

	var target patchTarget
	pRoleTarget := make([]patchRoleTarget, 0, len(paths))

	for _, path := range paths {
		value, err := clusterConfig.RawConfig.Get(path.path)
		var notExistErr libcluster.NotExistError
		if err != nil && !errors.As(err, &notExistErr) {
			return err
		}

		var updatedRoles []string
		if value != nil {
			updatedRoles, err = parseRoles(value)
			if err != nil {
				return err
			}
		}
		if updatedRoles, err = updateRolesFunc(updatedRoles, ctx.RoleName); err != nil {
			return fmt.Errorf("cannot update roles by path %s: %s", path.path, err)
		}

		targets, err := getCConfigPatchTargets(configData, path.path, path.depth)
		if err != nil {
			return err
		}
		target, err = c.pickTarget(targets, ctx.Force, strings.Join(path.path, "/"))
		if err != nil {
			return err
		}

		pRoleTarget = append(pRoleTarget, patchRoleTarget{
			path:      path.path,
			roleNames: updatedRoles,
		})
	}

	patched, err := patchFunc(target.config, pRoleTarget)
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

// ChangeRole patches a config to add role to a config.
func (c *CConfigSource) ChangeRole(ctx RolesChangeCtx, changeRoleFunc ChangeRoleFunc) error {
	return c.patchConfigWithRoles(ctx, getCConfigRolesPath, changeRoleFunc, patchCConfigEditRole)
}

// getCConfigRolesPath returns a path and it's minimum interesting depth
// to patch the config for role addition.
func getCConfigRolesPath(clusterConfig libcluster.ClusterConfig,
	ctx RolesChangeCtx) ([]path, error) {
	var paths []path
	if ctx.IsGlobal {
		paths = append(paths, path{
			path:  []string{"roles"},
			depth: 0,
		})
	}
	if ctx.GroupName != "" {
		p := []string{"groups", ctx.GroupName}
		if _, err := clusterConfig.RawConfig.Get(p); err != nil {
			var notExistErr libcluster.NotExistError
			if errors.As(err, &notExistErr) {
				return []path{}, fmt.Errorf("cannot find group %q", ctx.GroupName)
			}
			return []path{}, fmt.Errorf("failed to build a group path: %w", err)
		}
		paths = append(paths, path{
			path:  append(p, "roles"),
			depth: len(p),
		})
	}
	if ctx.ReplicasetName != "" {
		var group string
		var ok bool
		if group, ok = libcluster.FindGroupByReplicaset(clusterConfig, ctx.ReplicasetName); !ok {
			return []path{}, fmt.Errorf("cannot find replicaset %q above group", ctx.ReplicasetName)
		}
		p := []string{"groups", group, "replicasets", ctx.ReplicasetName}
		paths = append(paths, path{
			path:  append(p, "roles"),
			depth: len(p),
		})
	}
	if ctx.InstName != "" {
		var group, replicaset string
		var ok bool
		if group, replicaset, ok = libcluster.FindInstance(clusterConfig, ctx.InstName); !ok {
			return []path{}, fmt.Errorf("cannot find instance %q above group and/or replicaset",
				ctx.InstName)
		}
		p := []string{"groups", group, "replicasets", replicaset, "instances", ctx.InstName}
		paths = append(paths, path{
			path:  append(p, "roles"),
			depth: len(p),
		})
	}
	return paths, nil
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
