package replicaset

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	goconfig "github.com/tarantool/go-config"

	"github.com/tarantool/tt/cli/cluster"
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
	keyPicker KeyPicker,
) *CConfigSource {
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

// collectCConfig fetches and merges the config data, returning both the
// individual data items and a merged goconfig.Config view.
func collectCConfig(
	collector libcluster.DataCollector,
) ([]libcluster.Data, goconfig.Config, error) {
	configData, err := collector.Collect()
	if err != nil {
		return nil, goconfig.Config{}, fmt.Errorf("failed to collect cluster config: %w", err)
	}

	// Build an individual goconfig.Config for each data item, then merge them
	// into a single view by feeding each as a collector in priority order.
	// This mirrors the semantics of NewYamlDataMergeCollector.
	ctx := context.Background()
	builder := goconfig.NewBuilder()
	builder = builder.WithoutValidation()
	builder = builder.WithInheritance(
		goconfig.Levels(goconfig.Global, "groups", "replicasets", "instances"),
		goconfig.WithInheritMerge("credentials", goconfig.MergeDeep),
	)

	for _, item := range configData {
		if len(item.Value) == 0 {
			continue
		}
		src, err := cluster.NewBytesSource("collector-item", item.Value)
		if err != nil {
			return nil, goconfig.Config{},
				fmt.Errorf("failed to decode config from %q: %w", item.Source, err)
		}
		builder = builder.AddCollector(src)
	}

	merged, errs := builder.Build(ctx)
	if len(errs) > 0 {
		return nil, goconfig.Config{}, fmt.Errorf("failed to build merged config: %w",
			errors.Join(errs...))
	}

	return configData, merged, nil
}

// pickTarget applies keyPicker to the targets slice and returns picked target.
func (c *CConfigSource) pickTarget(targets []patchTarget, force bool,
	pathMsg string,
) (patchTarget, error) {
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
	getPathFunc func(cconfigInstance) (goconfig.KeyPath, int, error),
	patchFunc func(*goconfig.MutableConfig, cconfigInstance) (*goconfig.MutableConfig, error),
) error {
	configData, goView, err := collectCConfig(c.collector)
	if err != nil {
		return err
	}
	inst, err := getCConfigInstance(goView, instanceName)
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
	patchedSnap := patched.Snapshot()
	b, err := patchedSnap.MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal patched config: %w", err)
	}
	err = c.publisher.Publish(target.key, target.revision, b)
	if err != nil {
		return fmt.Errorf("failed to publish the config: %w", err)
	}
	return nil
}

// patchConfigWithRoles runs a config patching pipeline with adding roles.
func (c *CConfigSource) patchConfigWithRoles(ctx RolesChangeCtx,
	getPathFunc func(clusterConfig goconfig.Config,
		ctx RolesChangeCtx) (paths []path, err error),
	updateRolesFunc func([]string, string) ([]string, error),
	patchFunc func(config *goconfig.MutableConfig, prt []patchRoleTarget) (*goconfig.MutableConfig, error),
) error {
	configData, goView, err := collectCConfig(c.collector)
	if err != nil {
		return err
	}
	paths, err := getPathFunc(goView, ctx)
	if err != nil {
		return err
	}

	var target patchTarget
	pRoleTarget := make([]patchRoleTarget, 0, len(paths))

	for _, path := range paths {
		var updatedRoles []string

		if val, ok := goView.Lookup(path.path); ok {
			var existing any
			if err := val.Get(&existing); err != nil {
				return fmt.Errorf("failed to get roles at path %s: %w", path.path, err)
			}
			updatedRoles, err = parseRoles(existing)
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
	patchedSnap2 := patched.Snapshot()
	b, err := patchedSnap2.MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal patched config: %w", err)
	}
	err = c.publisher.Publish(target.key, target.revision, b)
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
		func(config *goconfig.MutableConfig, inst cconfigInstance) (*goconfig.MutableConfig, error) {
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
		func(config *goconfig.MutableConfig, inst cconfigInstance) (*goconfig.MutableConfig, error) {
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
		func(config *goconfig.MutableConfig, inst cconfigInstance) (*goconfig.MutableConfig, error) {
			return patchCConfigExpel(config, inst)
		},
	)
}

// ChangeRole patches a config with addition/removing role.
func (c *CConfigSource) ChangeRole(ctx RolesChangeCtx, action RolesChangerAction) error {
	return c.patchConfigWithRoles(ctx, getCConfigRolesPath, action.Change, patchCConfigEditRole)
}

// getCConfigRolesPath returns a path and it's minimum interesting depth
// to patch the config for role addition.
func getCConfigRolesPath(goView goconfig.Config,
	ctx RolesChangeCtx,
) ([]path, error) {
	var paths []path
	if ctx.IsGlobal {
		paths = append(paths, path{
			path:  goconfig.NewKeyPath("roles"),
			depth: 0,
		})
	}
	if ctx.GroupName != "" {
		p := goconfig.NewKeyPath(fmt.Sprintf("groups/%s", ctx.GroupName))
		if _, ok := goView.Lookup(p); !ok {
			return []path{}, fmt.Errorf("cannot find group %q", ctx.GroupName)
		}
		paths = append(paths, path{
			path:  append(p, "roles"),
			depth: len(p),
		})
	}
	if ctx.ReplicasetName != "" {
		var group string
		var ok bool
		if group, ok = cluster.FindGroupByReplicaset(goView, ctx.ReplicasetName); !ok {
			return []path{}, fmt.Errorf("cannot find replicaset %q above group", ctx.ReplicasetName)
		}
		p := goconfig.NewKeyPath(fmt.Sprintf("groups/%s/replicasets/%s", group, ctx.ReplicasetName))
		paths = append(paths, path{
			path:  append(p, "roles"),
			depth: len(p),
		})
	}
	if ctx.InstName != "" {
		var group, replicaset string
		var ok bool
		if group, replicaset, ok = cluster.FindInstance(goView, ctx.InstName); !ok {
			return []path{}, fmt.Errorf("cannot find instance %q above group and/or replicaset",
				ctx.InstName)
		}
		p := goconfig.NewKeyPath(fmt.Sprintf(
			"groups/%s/replicasets/%s/instances/%s", group, replicaset, ctx.InstName))
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
// * "/groups/g/replicasets/r".
func getCConfigPromotePath(inst cconfigInstance) (path goconfig.KeyPath, depth int, err error) {
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	switch failover {
	case FailoverOff:
		path = goconfig.NewKeyPath(fmt.Sprintf(
			"groups/%s/replicasets/%s/instances/%s/database/mode",
			groupName, replicasetName, instName))
		depth = len(path) - 2
	case FailoverManual:
		path = goconfig.NewKeyPath(fmt.Sprintf(
			"groups/%s/replicasets/%s/leader",
			groupName, replicasetName))
		depth = len(path) - 1
	case FailoverElection:
		err = fmt.Errorf(`unsupported failover: %q, supported: "manual", "off"`, failover)
	default:
		err = fmt.Errorf(`unknown failover, supported: "manual", "off"`)
	}
	return path, depth, err
}

// getCConfigDemotePath returns a path and it's minimum interesting depth
// to patch the config for instance demoting.
func getCConfigDemotePath(inst cconfigInstance) (path goconfig.KeyPath, depth int, err error) {
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	switch failover {
	case FailoverOff:
		path = goconfig.NewKeyPath(fmt.Sprintf(
			"groups/%s/replicasets/%s/instances/%s/database/mode",
			groupName, replicasetName, instName))
		depth = len(path) - 2
	case FailoverManual, FailoverElection:
		err = fmt.Errorf(`unsupported failover: %q, supported: "off"`, failover)
	default:
		err = fmt.Errorf(`unknown failover, supported: "off"`)
	}
	return path, depth, err
}

// getCConfigExpelPath returns a path and it's minimum interesting depth
// to patch the config for instance expelling.
func getCConfigExpelPath(inst cconfigInstance) (path goconfig.KeyPath, depth int, err error) {
	var (
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	path = goconfig.NewKeyPath(fmt.Sprintf(
		"groups/%s/replicasets/%s/instances/%s/iproto/listen",
		groupName, replicasetName, instName))
	depth = len(path) - 2
	return
}

// patchTarget describes a cluster config patch target.
type patchTarget struct {
	key      string
	revision int64
	config   *goconfig.MutableConfig
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
	path goconfig.KeyPath, depth int,
) ([]patchTarget, error) {
	var targets []patchTarget
	for _, item := range data {
		mut, err := cluster.BuildMutableFromBytes(context.Background(), item.Value)
		if err != nil {
			return nil,
				fmt.Errorf("failed to decode config from %q: %w", item.Source, err)
		}
		snap := mut.Snapshot()
		depth, err := getCConfigPathDepth(snap, path, depth)
		if err != nil {
			return nil, err
		}
		if depth != noDepth {
			targets = append(targets, patchTarget{
				key:      item.Source,
				revision: item.Revision,
				config:   mut,
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
func getCConfigPathDepth(config goconfig.Config,
	path goconfig.KeyPath, lowerDepth int,
) (int, error) {
	for i := len(path); i >= lowerDepth; i-- {
		if _, ok := config.Lookup(path[:i]); ok {
			return i, nil
		}
	}
	return noDepth, nil
}
