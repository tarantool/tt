package replicaset

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/apex/log"
	"github.com/mitchellh/mapstructure"
	goconfig "github.com/tarantool/go-config"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"

	libcluster "github.com/tarantool/tt/lib/cluster"
	integrityPkg "github.com/tarantool/tt/lib/integrity"
)

var (
	//go:embed lua/cconfig/get_instance_topology_body.lua
	cconfigGetInstanceTopologyBody string

	//go:embed lua/cconfig/promote_election.lua
	cconfigPromoteElectionBody string

	//go:embed lua/cconfig/bootstrap_vshard_body.lua
	cconfigBootstrapVShardBody string

	//go:embed lua/cconfig/get_sharding_roles_body.lua
	cconfigGetShardingRolesBody string
)

// cconfigTopology used to export topology information from a Tarantool
// instance with the centralized config orchestrator.
type cconfigTopology struct {
	// UUID is a current replicaset UUID.
	UUID string
	// LeaderUUID is a leader UUID in the replicaset.
	LeaderUUID string
	// Alias is a short name of the replicaset.
	Alias string
	// Failover is a string representation of a failover.
	Failover string
	// Instances is a list of known instances in a replicaset.
	Instances []Instance
	// InstanceUUID is a current instance UUID.
	InstanceUUID string
	// InstanceRW is true when the current instance is in RW mode.
	InstanceRW bool
}

// cconfigInstance describes an instance in the cluster config.
type cconfigInstance struct {
	failover       Failover
	groupName      string
	replicasetName string
	name           string
}

// CConfigInstance is an instance with the centralized config orchestrator.
type CConfigInstance struct {
	cachedDiscoverer
	evaler connector.Evaler
}

// patchRoleTarget describes a role content to patch a config.
type patchRoleTarget struct {
	// path is a destination to a patch target in config.
	path []string
	// roleNames are roles to set by path in config.
	roleNames []string
}

// NewCConfigInstance create a new CConfigInstance object for the evaler.
func NewCConfigInstance(evaler connector.Evaler) *CConfigInstance {
	inst := &CConfigInstance{
		evaler: evaler,
	}
	inst.discoverer = inst
	return inst
}

// discovery returns a replicasets topology for a single instance with
// the centralized config orchestrator.
func (c *CConfigInstance) discovery() (Replicasets, error) {
	topology, err := getCConfigInstanceTopology(c.evaler)
	if err != nil {
		return Replicasets{}, err
	}

	return recalculateMasters(Replicasets{
		State:        StateBootstrapped,
		Orchestrator: OrchestratorCentralizedConfig,
		Replicasets: []Replicaset{
			{
				UUID:       topology.UUID,
				LeaderUUID: topology.LeaderUUID,
				Alias:      topology.Alias,
				Failover:   ParseFailover(topology.Failover),
				Instances:  topology.Instances,
			},
		},
	}), nil
}

// Promote promotes an instance.
func (c *CConfigInstance) Promote(ctx PromoteCtx) error {
	return cconfigPromoteElection(c.evaler, ctx.Timeout)
}

// Demote is not supported for a single instance by the centralized config
// orchestrator.
func (c *CConfigInstance) Demote(ctx DemoteCtx) error {
	return newErrDemoteByInstanceNotSupported(OrchestratorCentralizedConfig)
}

// Expel is not supported for a single instance by the centralized config
// orchestrator.
func (c *CConfigInstance) Expel(ctx ExpelCtx) error {
	return newErrExpelByInstanceNotSupported(OrchestratorCentralizedConfig)
}

// Bootstrap is not supported for a single instance by the centralized config
// orchestrator.
func (c *CConfigInstance) Bootstrap(BootstrapCtx) error {
	return newErrBootstrapByInstanceNotSupported(OrchestratorCentralizedConfig)
}

// BootstrapVShard bootstraps vshard for a single instance by the centralized config
// orchestrator.
func (c *CConfigInstance) BootstrapVShard(ctx VShardBootstrapCtx) error {
	err := cconfigBootstrapVShard(c.evaler, ctx.Timeout)
	if err != nil {
		return err
	}
	return nil
}

// RolesChange is not supported for a single instance by the centralized config
// orchestrator.
func (c *CConfigInstance) RolesChange(ctx RolesChangeCtx,
	changeRoleAction RolesChangerAction,
) error {
	return newErrRolesChangeByInstanceNotSupported(OrchestratorCentralizedConfig, changeRoleAction)
}

// CConfigApplication is an application with the centralized config
// orchestrator.
type CConfigApplication struct {
	cachedDiscoverer
	runningCtx running.RunningCtx
	publishers libcluster.DataPublisherFactory
	collectors libcluster.DataCollectorFactory
	integ      integrityPkg.IntegrityCtx
}

// NewCConfigApplication creates a new CConfigApplication object.
func NewCConfigApplication(
	runningCtx running.RunningCtx,
	collectors libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory,
	integ integrityPkg.IntegrityCtx,
) *CConfigApplication {
	app := &CConfigApplication{
		runningCtx: runningCtx,
		publishers: publishers,
		collectors: collectors,
		integ:      integ,
	}
	app.discoverer = app
	return app
}

// discovery returns a replicasets topology for an application with
// the centralized config orchestrator.
func (c *CConfigApplication) discovery() (Replicasets, error) {
	var topologies []cconfigTopology

	err := EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
		func(ictx running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			topology, err := getCConfigInstanceTopology(evaler)
			if err != nil {
				return true, err
			}
			for i := range topology.Instances {
				if topology.Instances[i].UUID == topology.InstanceUUID {
					topology.Instances[i].InstanceCtx = ictx
					topology.Instances[i].InstanceCtxFound = true
				}
			}

			topologies = append(topologies, topology)
			return false, nil
		}))
	if err != nil {
		return Replicasets{}, err
	}

	if len(topologies) == 0 {
		return Replicasets{}, fmt.Errorf("no instance found in the application")
	}

	return mergeCConfigTopologies(topologies)
}

// Expel expels an instance from the centralized config's replicasets.
func (c *CConfigApplication) Expel(ctx ExpelCtx) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return fmt.Errorf("failed to get replicasets: %s", err)
	}

	targetReplicaset, targetInstance, found := findInstanceByAlias(replicasets, ctx.InstName)
	if !found {
		return fmt.Errorf("instance %q not found in a configured replicaset", ctx.InstName)
	}
	if !targetInstance.InstanceCtxFound {
		return fmt.Errorf("instance %q should be online", ctx.InstName)
	}
	if len(targetReplicaset.Instances) == 1 {
		return fmt.Errorf("not found any other instance joined to a replicaset")
	}

	var instances []running.InstanceCtx
	var unavailable []string
	for _, inst := range targetReplicaset.Instances {
		if !inst.InstanceCtxFound {
			if inst.Alias != ctx.InstName {
				// The target instance could be offline.
				unavailable = append(unavailable, inst.Alias)
			}
		} else {
			instances = append(instances, inst.InstanceCtx)
		}
	}
	if len(unavailable) > 0 {
		msg := fmt.Sprintf("could not connect to: %s", strings.Join(unavailable, ","))
		if !ctx.Force {
			return fmt.Errorf(
				"all other instances in the target replicaset should be online, %s", msg)
		}
		log.Warn(msg)
	}

	isConfigPublished, err := c.expel(targetInstance.InstanceCtx, ctx.InstName)
	// Check the config was published.
	if isConfigPublished {
		err = errors.Join(err, reloadCConfig(instances))
	}
	return err
}

// getCConfigInstanceTopology returns a topology for an instance.
func getCConfigInstanceTopology(evaler connector.Evaler) (cconfigTopology, error) {
	var topology cconfigTopology

	args := []any{}
	opts := connector.RequestOpts{}
	data, err := evaler.Eval(cconfigGetInstanceTopologyBody, args, opts)
	if err != nil {
		return topology, err
	}

	if len(data) != 1 {
		return topology, fmt.Errorf("unexpected response: %v", data)
	}

	if err := mapstructure.Decode(data[0], &topology); err != nil {
		return topology, fmt.Errorf("failed to parse a response: %w", err)
	}

	for i := range topology.Instances {
		if topology.Instances[i].UUID == topology.InstanceUUID {
			if topology.InstanceRW {
				topology.Instances[i].Mode = ModeRW
			} else {
				topology.Instances[i].Mode = ModeRead
			}
		}
	}

	return topology, nil
}

// mergeCConfigTopologies merges centralized config topologies per an
// instance into a Replicasets object.
func mergeCConfigTopologies(topologies []cconfigTopology) (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateBootstrapped,
		Orchestrator: OrchestratorCentralizedConfig,
	}

	for _, topology := range topologies {
		var replicaset *Replicaset
		for i := range replicasets.Replicasets {
			if topology.UUID == replicasets.Replicasets[i].UUID {
				replicaset = &replicasets.Replicasets[i]
				break
			}
		}

		if replicaset != nil {
			updateCConfigInstances(replicaset, topology)
		} else {
			replicasets.Replicasets = append(replicasets.Replicasets, Replicaset{
				UUID:       topology.UUID,
				LeaderUUID: topology.LeaderUUID,
				Alias:      topology.Alias,
				Roles:      []string{},
				Failover:   ParseFailover(topology.Failover),
				Instances:  topology.Instances,
			})
		}
	}

	// Clear expelled instances.
	for i := range replicasets.Replicasets {
		// spell-checker:ignore unexpelled
		unexpelled := []Instance{}
		for _, instance := range replicasets.Replicasets[i].Instances {
			if instance.URI != "" {
				unexpelled = append(unexpelled, instance)
			}
		}
		replicasets.Replicasets[i].Instances = unexpelled
	}

	return recalculateMasters(replicasets), nil
}

// updateCConfigInstances updates a configuration config instances in the
// replicaset according to the instance topology.
func updateCConfigInstances(replicaset *Replicaset, topology cconfigTopology) {
	for _, tinstance := range topology.Instances {
		var instance *Instance
		for i := range replicaset.Instances {
			if tinstance.UUID == replicaset.Instances[i].UUID {
				instance = &replicaset.Instances[i]
			}
		}
		if instance != nil {
			if instance.URI == "" {
				instance.URI = tinstance.URI
			}
			if instance.Mode == ModeUnknown {
				instance.Mode = tinstance.Mode
			}
			if !instance.InstanceCtxFound {
				instance.InstanceCtx = tinstance.InstanceCtx
				instance.InstanceCtxFound = tinstance.InstanceCtxFound
			}
		} else {
			replicaset.Instances = append(replicaset.Instances, tinstance)
		}
	}
}

// Promote promotes an instance in the application.
func (c *CConfigApplication) Promote(ctx PromoteCtx) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return fmt.Errorf("failed to get replicasets: %w", err)
	}
	targetReplicaset, targetInstance, found := findInstanceByAlias(replicasets, ctx.InstName)
	if !found {
		return fmt.Errorf("instance %q not found in a configured replicaset", ctx.InstName)
	}
	if !targetInstance.InstanceCtxFound {
		return fmt.Errorf("instance %q should be online", ctx.InstName)
	}

	var instances []running.InstanceCtx
	var unavailable []string
	for _, inst := range targetReplicaset.Instances {
		if !inst.InstanceCtxFound {
			unavailable = append(unavailable, inst.Alias)
		} else {
			instances = append(instances, inst.InstanceCtx)
		}
	}
	if len(unavailable) > 0 {
		msg := fmt.Sprintf("could not connect to: %s", strings.Join(unavailable, ","))
		if !ctx.Force {
			return fmt.Errorf("all instances in the target replicaset should be online, %s", msg)
		}
		log.Warn(msg)
	}

	isConfigPublished, err := c.promote(targetInstance, ctx)
	// Check the config was published.
	if isConfigPublished {
		err = errors.Join(err, reloadCConfig(instances))
	}
	return err
}

// Demote demotes an instance in the application.
func (c *CConfigApplication) Demote(ctx DemoteCtx) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return fmt.Errorf("failed to get replicasets: %w", err)
	}
	targetReplicaset, targetInstance, found := findInstanceByAlias(replicasets, ctx.InstName)
	if !found {
		return fmt.Errorf("instance %q not found in a configured replicaset", ctx.InstName)
	}
	if !targetInstance.InstanceCtxFound {
		return fmt.Errorf("instance %q should be online", ctx.InstName)
	}

	var instances []running.InstanceCtx
	var unavailable []string
	for _, inst := range targetReplicaset.Instances {
		if !inst.InstanceCtxFound {
			unavailable = append(unavailable, inst.Alias)
		} else {
			instances = append(instances, inst.InstanceCtx)
		}
	}
	if len(unavailable) > 0 {
		msg := fmt.Sprintf("could not connect to: %s", strings.Join(unavailable, ","))
		if !ctx.Force {
			return fmt.Errorf("all instances in the target replicaset should be online, %s", msg)
		}
		log.Warn(msg)
	}

	isConfigPublished, err := c.demote(targetInstance, targetReplicaset, ctx)
	// Check the config was published.
	if isConfigPublished {
		err = errors.Join(err, reloadCConfig(instances))
	}
	return err
}

// BootstrapVShard bootstraps vshard for an application by the centralized config orchestrator.
func (c *CConfigApplication) BootstrapVShard(ctx VShardBootstrapCtx) error {
	var (
		lastErr error
		found   bool
	)
	eval := func(instance running.InstanceCtx, evaler connector.Evaler) (bool, error) {
		roles, err := cconfigGetShardingRoles(evaler)
		if err != nil {
			lastErr = fmt.Errorf("failed to get sharding roles: %w", err)
			// Try again with another instance.
			return false, nil
		}
		isRouter := false
		for _, role := range roles {
			if role == "router" {
				isRouter = true
				break
			}
		}
		if !isRouter {
			// Try again with another instance.
			return false, nil
		}
		found = true
		lastErr = cconfigBootstrapVShard(evaler, ctx.Timeout)
		return lastErr == nil, nil
	}
	err := EvalForeach(c.runningCtx.Instances, InstanceEvalFunc(eval))
	for _, e := range []error{err, lastErr} {
		if e != nil {
			return e
		}
	}
	if !found {
		return fmt.Errorf("not found any vshard router in replicaset")
	}
	return nil
}

// Bootstrap is not supported for an application by the centralized config
// orchestrator.
func (c *CConfigApplication) Bootstrap(BootstrapCtx) error {
	return newErrBootstrapByAppNotSupported(OrchestratorCentralizedConfig)
}

// RolesChange adds/removes role for an application by the centralized config orchestrator.
func (c *CConfigApplication) RolesChange(ctx RolesChangeCtx,
	changeRoleAction RolesChangerAction,
) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return fmt.Errorf("failed to get replicasets: %w", err)
	}

	var (
		instances   []running.InstanceCtx
		unavailable []string
	)

	if ctx.InstName != "" {
		targetReplicaset, targetInstance, found :=
			findInstanceByAlias(replicasets, ctx.InstName)
		if !found {
			return fmt.Errorf("instance %q not found in a configured replicaset", ctx.InstName)
		}
		if !targetInstance.InstanceCtxFound {
			return fmt.Errorf("instance %q should be online", ctx.InstName)
		}
		for _, inst := range targetReplicaset.Instances {
			if !inst.InstanceCtxFound {
				unavailable = append(unavailable, inst.Alias)
				continue
			}
			instances = append(instances, inst.InstanceCtx)
		}
	} else {
		for _, r := range c.replicasets.Replicasets {
			for _, i := range r.Instances {
				if !i.InstanceCtxFound {
					unavailable = append(unavailable, i.Alias)
					continue
				}
				instances = append(instances, i.InstanceCtx)
			}
		}
	}
	if len(unavailable) > 0 {
		msg := "could not connect to: " + strings.Join(unavailable, ",")
		if !ctx.Force {
			return fmt.Errorf("all instances in the target replicaset should be online, %s", msg)
		}
		log.Warn(msg)
	}

	isConfigPublished, err := c.rolesChange(ctx, changeRoleAction)
	if isConfigPublished {
		err = errors.Join(err, reloadCConfig(instances))
	}
	return err
}

// cconfigPromoteElection tries to promote an instance via `box.ctl.promote()`.
func cconfigPromoteElection(evaler connector.Evaler, timeout int) error {
	args := []any{}
	opts := connector.RequestOpts{}
	_, err := evaler.Eval(cconfigPromoteElectionBody, args, opts)
	if err != nil {
		return fmt.Errorf("failed to promote via election: %w", err)
	}
	return waitRW(evaler, timeout)
}

// cconfigBootstrapVShard bootstraps vshard on the passed instance.
func cconfigBootstrapVShard(evaler connector.Evaler, timeout int) error {
	var opts connector.RequestOpts
	_, err := evaler.Eval(cconfigBootstrapVShardBody, []any{timeout}, opts)
	if err != nil {
		return fmt.Errorf("failed to bootstrap vshard: %w", err)
	}
	return nil
}

// cconfigGetShardingRoles returns sharding roles of the passed instance.
func cconfigGetShardingRoles(evaler connector.Evaler) ([]string, error) {
	var opts connector.RequestOpts
	resp, err := evaler.Eval(cconfigGetShardingRolesBody, []any{}, opts)
	if err != nil {
		return nil, err
	}
	if len(resp) != 1 {
		return nil, fmt.Errorf("unexpected response length: %d", len(resp))
	}
	rolesAnyArray, ok := resp[0].([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response type: %T", resp[0])
	}
	var ret []string
	for _, role := range rolesAnyArray {
		if roleStr, ok := role.(string); ok {
			ret = append(ret, roleStr)
		} else {
			return nil, fmt.Errorf("unexpected role type: %T", role)
		}
	}
	return ret, nil
}

// reloadCConfig reloads a cluster config on the several instances.
func reloadCConfig(instances []running.InstanceCtx) error {
	errored := []string{}
	eval := func(instance running.InstanceCtx, evaler connector.Evaler) (bool, error) {
		args := []any{}
		opts := connector.RequestOpts{}
		_, err := evaler.Eval("require('config'):reload()", args, opts)
		if err != nil {
			fmt.Println(err)
			errored = append(errored, instance.InstName)
		}
		return false, nil
	}
	if err := EvalForeach(instances, InstanceEvalFunc(eval)); err != nil {
		return fmt.Errorf("failed to reload instances configuration"+
			", please try to do it manually with `require('config'):reload()`: %w", err)
	}
	if len(errored) > 0 {
		return fmt.Errorf("failed to reload instance configuration for: %s, "+
			"please try to do it manually with `require('config'):reload()`",
			strings.Join(errored, ", "))
	}
	return nil
}

// promote promotes an instance in the application and returns true
// if the instance config was published.
func (c *CConfigApplication) promote(instance Instance,
	ctx PromoteCtx,
) (wasConfigPublished bool, err error) {
	clusterCfgPath := instance.InstanceCtx.ClusterConfigPath
	clusterCfg, err := cluster.GetClusterConfig(context.Background(), clusterCfgPath, c.integ)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster config: %w", err)
	}
	goView := clusterCfg.Snapshot()

	inst, err := getCConfigInstance(goView, ctx.InstName)
	if err != nil {
		return false, err
	}

	if inst.failover == FailoverElection {
		eval := func(_ running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			return true, cconfigPromoteElection(evaler, ctx.Timeout)
		}
		err := EvalAny([]running.InstanceCtx{instance.InstanceCtx}, InstanceEvalFunc(eval))
		return false, err
	}

	err = patchLocalCConfig(
		clusterCfgPath,
		c.collectors,
		c.publishers,
		func(config *goconfig.MutableConfig) (*goconfig.MutableConfig, error) {
			return patchCConfigPromote(config, inst)
		},
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

// demote demotes an instance in the application and returns true
// if the instance config was published.
func (c *CConfigApplication) demote(instance Instance,
	replicaset Replicaset, ctx DemoteCtx,
) (wasConfigPublished bool, err error) {
	clusterCfgPath := instance.InstanceCtx.ClusterConfigPath
	clusterCfg, err := cluster.GetClusterConfig(context.Background(), clusterCfgPath, c.integ)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster config: %w", err)
	}
	goView := clusterCfg.Snapshot()

	cconfigInstance, err := getCConfigInstance(goView, ctx.InstName)
	if err != nil {
		return false, err
	}

	if cconfigInstance.failover == FailoverElection {
		electionMode, err := cconfigGetElectionMode(goView, ctx.InstName)
		if err != nil {
			return false, err
		}
		if electionMode != ElectionModeCandidate {
			return false,
				fmt.Errorf(`unexpected election_mode: %q, "candidate" expected`, electionMode)
		}
		if replicaset.LeaderUUID != instance.UUID {
			return false,
				fmt.Errorf("an instance must be the leader of the replicaset to demote it")
		}
		return c.demoteElection(instance.InstanceCtx, cconfigInstance, ctx.Timeout)
	}

	err = patchLocalCConfig(
		clusterCfgPath,
		c.collectors,
		c.publishers,
		func(config *goconfig.MutableConfig) (*goconfig.MutableConfig, error) {
			return patchCConfigDemote(config, cconfigInstance)
		},
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

// expel expels an instance in the application and returns true
// if the instance config was published.
func (c *CConfigApplication) expel(instance running.InstanceCtx, name string) (bool, error) {
	clusterCfgPath := instance.ClusterConfigPath
	clusterCfg, err := cluster.GetClusterConfig(context.Background(), clusterCfgPath, c.integ)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster config: %w", err)
	}
	goView := clusterCfg.Snapshot()

	cconfigInstance, err := getCConfigInstance(goView, name)
	if err != nil {
		return false, err
	}

	err = patchLocalCConfig(
		clusterCfgPath,
		c.collectors,
		c.publishers,
		func(config *goconfig.MutableConfig) (*goconfig.MutableConfig, error) {
			return patchCConfigExpel(config, cconfigInstance)
		},
	)
	if err != nil {
		return false, err
	}
	return true, err
}

// demoteElection demotes an instance in the replicaset with "election" failover.
// https://github.com/tarantool/tarantool/issues/9855
func (c *CConfigApplication) demoteElection(instanceCtx running.InstanceCtx,
	cconfigInstance cconfigInstance, timeout int,
) (wasConfigPublished bool, err error) {
	// Set election_mode: "voter" on the target instance.
	err = patchLocalCConfig(
		instanceCtx.ClusterConfigPath,
		c.collectors,
		c.publishers,
		func(config *goconfig.MutableConfig) (*goconfig.MutableConfig, error) {
			return patchCConfigElectionMode(config, cconfigInstance, ElectionModeVoter)
		},
	)
	if err != nil {
		return wasConfigPublished, err
	}

	wasConfigPublished = true
	if err = reloadCConfig([]running.InstanceCtx{instanceCtx}); err != nil {
		return wasConfigPublished, err
	}
	// Wait until an other instance is not elected.
	evalWaitRo := func(_ running.InstanceCtx,
		evaler connector.Evaler,
	) (bool, error) {
		return true, waitRO(evaler, timeout)
	}
	err = EvalAny([]running.InstanceCtx{instanceCtx}, InstanceEvalFunc(evalWaitRo))
	if err != nil {
		return wasConfigPublished, err
	}
	// Restore election_mode: "candidate" on the target instance.
	err = patchLocalCConfig(
		instanceCtx.ClusterConfigPath,
		c.collectors,
		c.publishers,
		func(config *goconfig.MutableConfig) (*goconfig.MutableConfig, error) {
			return patchCConfigElectionMode(config, cconfigInstance, ElectionModeCandidate)
		},
	)
	return wasConfigPublished, err
}

func (c *CConfigApplication) rolesChange(ctx RolesChangeCtx,
	action RolesChangerAction,
) (bool, error) {
	if len(c.runningCtx.Instances) == 0 {
		return false, fmt.Errorf("there are no running instances")
	}
	clusterCfgPath := c.runningCtx.Instances[0].ClusterConfigPath

	clusterCfg, err := cluster.GetClusterConfig(context.Background(), clusterCfgPath, c.integ)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster config: %w", err)
	}
	goView := clusterCfg.Snapshot()

	paths, err := getCConfigRolesPath(goView, ctx)
	if err != nil {
		return false, err
	}

	pRoleTarget := make([]patchRoleTarget, 0, len(paths))
	for _, path := range paths {
		var existingRoles []string
		if val, ok := goView.Lookup(path.path); ok {
			var existing any
			if err := val.Get(&existing); err != nil {
				return false, fmt.Errorf("failed to get roles at path %s: %w", path.path, err)
			}
			existingRoles, err = parseRoles(existing)
			if err != nil {
				return false, err
			}
		}

		existingRoles, err = action.Change(existingRoles, ctx.RoleName)
		if err != nil {
			return false, fmt.Errorf("failed to change roles: %w", err)
		}

		pRoleTarget = append(pRoleTarget, patchRoleTarget{
			path:      path.path,
			roleNames: existingRoles,
		})
	}
	if err := patchLocalCConfig(
		clusterCfgPath,
		c.collectors,
		c.publishers,
		func(config *goconfig.MutableConfig) (*goconfig.MutableConfig, error) {
			return patchCConfigEditRole(config, pRoleTarget)
		},
	); err != nil {
		return false, err
	}
	return true, nil
}

// patchLocalCConfig patches the local cluster config file.
//
// It reads the file directly via os.ReadFile, builds a *goconfig.MutableConfig,
// runs patchFunc on it, marshals the result and publishes (overwrites) the file.
func patchLocalCConfig(clusterCfgPath string,
	_ libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory,
	patchFunc func(*goconfig.MutableConfig) (*goconfig.MutableConfig, error),
) error {
	data, err := os.ReadFile(clusterCfgPath)
	if err != nil {
		return fmt.Errorf("failed to read cluster config %q: %w", clusterCfgPath, err)
	}

	mut, err := cluster.BuildMutableFromBytes(context.Background(), data)
	if err != nil {
		return fmt.Errorf("failed to build mutable config from %q: %w", clusterCfgPath, err)
	}

	mut, err = patchFunc(mut)
	if err != nil {
		return fmt.Errorf("failed to patch config: %w", err)
	}

	mutSnap := mut.Snapshot()
	b, err := mutSnap.MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal patched config: %w", err)
	}

	publisher, err := publishers.NewFile(clusterCfgPath)
	if err != nil {
		return fmt.Errorf("failed to create a configuration publisher: %w", err)
	}
	return publisher.Publish(0, b)
}

// cconfigGetFailover extracts the instance replicaset failover.
func cconfigGetFailover(cfg goconfig.Config, instName string) (Failover, error) {
	instCfg, err := cluster.InstanceConfig(cfg, instName)
	if err != nil {
		return FailoverOff, fmt.Errorf("failed to get instance config for failover: %w", err)
	}

	var raw any
	if _, err = instCfg.Get(goconfig.NewKeyPath("replication/failover"), &raw); err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			// Path not found. Check whether "replication" exists but is not a
			// map (e.g. replication: 42), to preserve the original error message.
			if repVal, ok := instCfg.Lookup(goconfig.NewKeyPath("replication")); ok {
				var repMap map[string]any
				if innerErr := repVal.Get(&repMap); innerErr != nil {
					return FailoverOff,
						fmt.Errorf(`path ["replication"] is not a map`)
				}
			}
			// https://github.com/tarantool/tt/issues/791
			return FailoverOff, nil
		}
		return FailoverOff, fmt.Errorf("failed to get failover: %w", err)
	}
	failoverStr, ok := raw.(string)
	if !ok {
		return FailoverOff,
			fmt.Errorf("unexpected failover type: %T, string expected", raw)
	}
	return ParseFailover(failoverStr), nil
}

// cconfigGetElectionMode extracts election_mode from the cluster config.
// If election_mode is not set, returns a default, which corresponds to the "election" failover.
func cconfigGetElectionMode(cfg goconfig.Config, instName string) (ElectionMode, error) {
	instCfg, err := cluster.InstanceConfig(cfg, instName)
	if err != nil {
		return ElectionModeCandidate,
			fmt.Errorf("failed to get instance config for election_mode: %w", err)
	}

	var raw any
	if _, err = instCfg.Get(goconfig.NewKeyPath("replication/election_mode"), &raw); err != nil {
		if errors.Is(err, goconfig.ErrKeyNotFound) {
			// This is true if failover == "election" && replica is not anonymous.
			// https://github.com/tarantool/tarantool/blob/e01fe8f7144eebc64249ab60a83f656cb4a11dc0/src/box/lua/config/applier/box_cfg.lua#L418-L420
			return ElectionModeCandidate, nil
		}
		return ElectionModeCandidate, fmt.Errorf("failed to get election_mode: %w", err)
	}
	electionModeStr, ok := raw.(string)
	if !ok {
		return ElectionModeCandidate,
			fmt.Errorf("unexpected election_mode type: %T, string expected", raw)
	}
	return ParseElectionMode(electionModeStr), nil
}

// clearEmptyMapFlowStyle removes the node from the config if it is currently
// an empty map (i.e. originally written as "path: {}").
//
// Nodes parsed from flow-style YAML (e.g. "instance-002: {}") carry a
// FlowStyle annotation that propagates to every child set via MutableConfig.Set,
// causing the marshaled output to collapse all nested keys onto a single line.
// Deleting the node before adding sub-paths creates a fresh block-style entry.
//
// When the node already has content (non-empty map) the annotation is already
// block-style and this function is a no-op.
func clearEmptyMapFlowStyle(config *goconfig.MutableConfig, path goconfig.KeyPath) {
	val, ok := config.Lookup(path)
	if !ok {
		// Node does not exist yet; nothing to clear.
		return
	}
	var m map[string]any
	if err := val.Get(&m); err != nil || len(m) != 0 {
		// Not an empty map: either type error or already has content.
		return
	}
	// Empty map — delete to drop the flow-style annotation.
	config.Delete(path)
}

// patchCConfigPromote patches the config to promote an instance.
func patchCConfigPromote(config *goconfig.MutableConfig,
	inst cconfigInstance,
) (*goconfig.MutableConfig, error) {
	var err error
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	switch failover {
	case FailoverOff:
		instPath := goconfig.NewKeyPath(fmt.Sprintf(
			"groups/%s/replicasets/%s/instances/%s", groupName, replicasetName, instName))
		clearEmptyMapFlowStyle(config, instPath)
		clearEmptyMapFlowStyle(config, instPath.Append("database"))
		err = config.Set(goconfig.NewKeyPath(fmt.Sprintf(
			"groups/%s/replicasets/%s/instances/%s/database/mode",
			groupName, replicasetName, instName)), "rw")
	case FailoverManual:
		err = config.Set(goconfig.NewKeyPath(fmt.Sprintf(
			"groups/%s/replicasets/%s/leader",
			groupName, replicasetName)), instName)
	default:
		return nil, fmt.Errorf("unexpected failover: %q", failover)
	}
	return config, err
}

// patchCConfigExpel patches the config to expel an instance, following the documentation:
// https://www.tarantool.io/en/doc/latest/how-to/replication/repl_bootstrap/#disconnecting-an-instance
//
// It set up:
// instance.iproto.listen = {}.
func patchCConfigExpel(config *goconfig.MutableConfig,
	inst cconfigInstance,
) (*goconfig.MutableConfig, error) {
	var (
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	instPath := goconfig.NewKeyPath(fmt.Sprintf(
		"groups/%s/replicasets/%s/instances/%s",
		groupName, replicasetName, instName))
	clearEmptyMapFlowStyle(config, instPath)
	listenPath := goconfig.NewKeyPath(fmt.Sprintf(
		"groups/%s/replicasets/%s/instances/%s/iproto/listen",
		groupName, replicasetName, instName))
	if err := config.Set(listenPath, map[string]any{}); err != nil {
		return nil, err
	}
	return config, nil
}

// patchCConfigDemote patches the config to demote an instance.
func patchCConfigDemote(config *goconfig.MutableConfig,
	inst cconfigInstance,
) (*goconfig.MutableConfig, error) {
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	if failover != FailoverOff {
		return nil, fmt.Errorf("unexpected failover: %q", failover)
	}
	instPath := goconfig.NewKeyPath(fmt.Sprintf(
		"groups/%s/replicasets/%s/instances/%s", groupName, replicasetName, instName))
	clearEmptyMapFlowStyle(config, instPath)
	clearEmptyMapFlowStyle(config, instPath.Append("database"))
	if err := config.Set(goconfig.NewKeyPath(fmt.Sprintf(
		"groups/%s/replicasets/%s/instances/%s/database/mode",
		groupName, replicasetName, instName)), "ro"); err != nil {
		return nil, err
	}
	return config, nil
}

// patchCConfigElectionMode patches the config to change an instance election_mode.
func patchCConfigElectionMode(config *goconfig.MutableConfig,
	inst cconfigInstance, mode ElectionMode,
) (*goconfig.MutableConfig, error) {
	instPath := goconfig.NewKeyPath(fmt.Sprintf(
		"groups/%s/replicasets/%s/instances/%s",
		inst.groupName, inst.replicasetName, inst.name))
	clearEmptyMapFlowStyle(config, instPath)
	err := config.Set(goconfig.NewKeyPath(fmt.Sprintf(
		"groups/%s/replicasets/%s/instances/%s/replication/election_mode",
		inst.groupName, inst.replicasetName, inst.name)), mode.String())
	if err != nil {
		return nil, err
	}
	return config, nil
}

func patchCConfigEditRole(config *goconfig.MutableConfig,
	targets []patchRoleTarget,
) (*goconfig.MutableConfig, error) {
	for _, p := range targets {
		// Delete before Set so that existing array children are cleared.
		// MutableConfig.Set does not remove children when the value is a
		// slice, so without Delete the old items persist in the YAML output.
		config.Delete(p.path)
		if err := config.Set(p.path, p.roleNames); err != nil {
			return nil, err
		}
	}
	return config, nil
}

// getCConfigInstance extracts an instance from the cluster config.
func getCConfigInstance(cfg goconfig.Config, instName string) (cconfigInstance, error) {
	inst := cconfigInstance{name: instName}

	g, r, found := cluster.FindInstance(cfg, instName)
	if !found {
		return inst, fmt.Errorf("instance %q not found in the cluster configuration", instName)
	}
	inst.groupName = g
	inst.replicasetName = r

	var err error
	inst.failover, err = cconfigGetFailover(cfg, instName)
	return inst, err
}
