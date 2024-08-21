package replicaset

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/apex/log"
	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"

	libcluster "github.com/tarantool/tt/lib/cluster"
)

var (
	//go:embed lua/cconfig/get_instance_topology_body.lua
	cconfigGetInstanceTopologyBody string

	//go:embed lua/cconfig/promote_election.lua
	cconfigPromoteElectionBody string

	//go:embed lua/cconfig/bootstrap_vshard_body.lua
	cconfigBootstrapVShardBody string

	cconfigGetShardingRolesBody = "return require('config'):get().sharding.roles"
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

// patchRoleTarget describes a content to patch a config.
type patchRoleTarget struct {
	// path is a destination to a patch target in config.
	path []string
	// roleNames are roles to add by path in config.
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
			Replicaset{
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

// CConfigApplication is an application with the centralized config
// orchestrator.
type CConfigApplication struct {
	cachedDiscoverer
	runningCtx running.RunningCtx
	publishers libcluster.DataPublisherFactory
	collectors libcluster.DataCollectorFactory
}

// NewCConfigApplication creates a new CConfigApplication object.
func NewCConfigApplication(
	runningCtx running.RunningCtx,
	collectors libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory) *CConfigApplication {
	app := &CConfigApplication{
		runningCtx: runningCtx,
		publishers: publishers,
		collectors: collectors,
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
			for i, _ := range topology.Instances {
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

// Expel expels an instance from the cetralized config's replicasets.
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
	var unfound []string
	for _, inst := range targetReplicaset.Instances {
		if !inst.InstanceCtxFound {
			if inst.Alias != ctx.InstName {
				// The target instance could be offline.
				unfound = append(unfound, inst.Alias)
			}
		} else {
			instances = append(instances, inst.InstanceCtx)
		}
	}
	if len(unfound) > 0 {
		msg := fmt.Sprintf("could not connect to: %s", strings.Join(unfound, ","))
		if !ctx.Force {
			return fmt.Errorf(
				"all other instances in the target replicast should be online, %s", msg)
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

	for i, _ := range topology.Instances {
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
		for i, _ := range replicasets.Replicasets {
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
	for i, _ := range replicasets.Replicasets {
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
		for i, _ := range replicaset.Instances {
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
	var unfound []string
	for _, inst := range targetReplicaset.Instances {
		if !inst.InstanceCtxFound {
			unfound = append(unfound, inst.Alias)
		} else {
			instances = append(instances, inst.InstanceCtx)
		}
	}
	if len(unfound) > 0 {
		msg := fmt.Sprintf("could not connect to: %s", strings.Join(unfound, ","))
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
	var unfound []string
	for _, inst := range targetReplicaset.Instances {
		if !inst.InstanceCtxFound {
			unfound = append(unfound, inst.Alias)
		} else {
			instances = append(instances, inst.InstanceCtx)
		}
	}
	if len(unfound) > 0 {
		msg := fmt.Sprintf("could not connect to: %s", strings.Join(unfound, ","))
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
	ctx PromoteCtx) (wasConfigPublished bool, err error) {
	cluterCfgPath := instance.InstanceCtx.ClusterConfigPath
	clusterCfg, err := cluster.GetClusterConfig(
		libcluster.NewCollectorFactory(c.collectors), cluterCfgPath)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster config: %w", err)
	}

	inst, err := getCConfigInstance(&clusterCfg, ctx.InstName)
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
		cluterCfgPath,
		c.collectors,
		c.publishers,
		func(config *libcluster.Config) (*libcluster.Config, error) {
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
	replicaset Replicaset, ctx DemoteCtx) (wasConfigPublished bool, err error) {
	cluterCfgPath := instance.InstanceCtx.ClusterConfigPath
	clusterCfg, err := cluster.GetClusterConfig(libcluster.NewCollectorFactory(c.collectors),
		cluterCfgPath)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster config: %w", err)
	}

	cconfigInstance, err := getCConfigInstance(&clusterCfg, ctx.InstName)
	if err != nil {
		return false, err
	}

	if cconfigInstance.failover == FailoverElection {
		electionMode, err := cconfigGetElectionMode(&clusterCfg, ctx.InstName)
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
		cluterCfgPath,
		c.collectors,
		c.publishers,
		func(config *libcluster.Config) (*libcluster.Config, error) {
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
	clusterCfg, err := cluster.GetClusterConfig(libcluster.NewCollectorFactory(c.collectors),
		clusterCfgPath)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster config: %w", err)
	}

	cconfigInstance, err := getCConfigInstance(&clusterCfg, name)
	if err != nil {
		return false, err
	}

	err = patchLocalCConfig(
		clusterCfgPath,
		c.collectors,
		c.publishers,
		func(config *libcluster.Config) (*libcluster.Config, error) {
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
	cconfigInstance cconfigInstance, timeout int) (wasConfigPublished bool, err error) {
	// Set election_mode: "voter" on the target instance.
	err = patchLocalCConfig(
		instanceCtx.ClusterConfigPath,
		c.collectors,
		c.publishers,
		func(config *libcluster.Config) (*libcluster.Config, error) {
			return patchCConfigElectionMode(config, cconfigInstance, ElectionModeVoter)
		},
	)
	if err != nil {
		return
	}

	wasConfigPublished = true
	if err = reloadCConfig([]running.InstanceCtx{instanceCtx}); err != nil {
		return
	}
	// Wait until an other instance is not elected.
	evalWaitRo := func(_ running.InstanceCtx,
		evaler connector.Evaler) (bool, error) {
		return true, waitRO(evaler, timeout)
	}
	err = EvalAny([]running.InstanceCtx{instanceCtx}, InstanceEvalFunc(evalWaitRo))
	if err != nil {
		return
	}
	// Restore election_mode: "candidate" on the target instance.
	err = patchLocalCConfig(
		instanceCtx.ClusterConfigPath,
		c.collectors,
		c.publishers,
		func(config *libcluster.Config) (*libcluster.Config, error) {
			return patchCConfigElectionMode(config, cconfigInstance, ElectionModeCandidate)
		},
	)
	return
}

// patchLocalConfig patches the local cluster config.
func patchLocalCConfig(path string,
	collectors libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory,
	patchFunc func(*libcluster.Config) (*libcluster.Config, error)) error {
	collector, publisher, err := cconfigCreateCollectorAndDataPublisher(
		collectors,
		publishers,
		path,
	)
	if err != nil {
		return err
	}
	config, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to collect a configuration to update: %w", err)
	}
	config, err = patchFunc(config)
	if err != nil {
		return fmt.Errorf("failed to patch config: %w", err)
	}

	err = libcluster.NewYamlConfigPublisher(publisher).Publish(config)
	return err
}

// cconfigCreateCollectorAndDataPublisher creates collector and data publisher
// for the local cluster config manipulations.
func cconfigCreateCollectorAndDataPublisher(
	collectors libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory,
	clusterCfgPath string) (libcluster.Collector, libcluster.DataPublisher, error) {
	collector, err := libcluster.NewCollectorFactory(collectors).NewFile(clusterCfgPath)
	if err != nil {
		return nil, nil,
			fmt.Errorf("failed to create a configuration collector: %w", err)
	}
	publisher, err := publishers.NewFile(clusterCfgPath)
	if err != nil {
		return nil, nil,
			fmt.Errorf("failed to create a configuration publisher: %w", err)
	}
	return collector, publisher, nil
}

// cconfigGetFailover extracts the instance replicaset failover.
func cconfigGetFailover(clusterConfig *libcluster.ClusterConfig,
	instName string) (Failover, error) {
	var failover Failover
	instConfig := libcluster.Instantiate(*clusterConfig, instName)

	rawFailover, err := instConfig.Get([]string{"replication", "failover"})
	var notExistErr libcluster.NotExistError
	if errors.As(err, &notExistErr) {
		// https://github.com/tarantool/tt/issues/791
		return FailoverOff, nil
	}
	if err != nil {
		return failover,
			fmt.Errorf("failed to get failover: %w", err)
	}
	failoverStr, ok := rawFailover.(string)
	if !ok {
		return failover,
			fmt.Errorf("unexpected failover type: %T, string expected", rawFailover)
	}
	failover = ParseFailover(failoverStr)
	return failover, nil
}

// cconfigGetElectionMode extracts election_mode from the cluster config.
// If election_mode is not set, returns a default, which corresponds to the "election" failover.
func cconfigGetElectionMode(clusterConfig *libcluster.ClusterConfig,
	instName string) (ElectionMode, error) {
	var electionMode ElectionMode
	instConfig := libcluster.Instantiate(*clusterConfig, instName)

	rawElectionMode, err := instConfig.Get([]string{"replication", "election_mode"})
	var notExistErr libcluster.NotExistError
	if errors.As(err, &notExistErr) {
		// This is true if failover == "election" && replica is not anonymous.
		// https://github.com/tarantool/tarantool/blob/e01fe8f7144eebc64249ab60a83f656cb4a11dc0/src/box/lua/config/applier/box_cfg.lua#L418-L420
		return ElectionModeCandidate, nil
	}
	if err != nil {
		return electionMode,
			fmt.Errorf("failed to get election_mode: %w", err)
	}
	electionModeStr, ok := rawElectionMode.(string)
	if !ok {
		return electionMode,
			fmt.Errorf("unexpected election_mode type: %T, string expected", rawElectionMode)
	}
	electionMode = ParseElectionMode(electionModeStr)
	return electionMode, nil
}

// patchCConfigPromote patches the config to promote an instance.
func patchCConfigPromote(config *libcluster.Config,
	inst cconfigInstance) (*libcluster.Config, error) {
	var err error
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	switch failover {
	case FailoverOff:
		err = config.Set([]string{"groups", groupName, "replicasets", replicasetName,
			"instances", instName, "database", "mode"}, "rw")
	case FailoverManual:
		err = config.Set([]string{"groups", groupName, "replicasets", replicasetName,
			"leader"}, instName)
	default:
		return nil, fmt.Errorf("unexpected failover: %q", failover)
	}
	return config, err
}

// patchCConfigExpel patches the config to expel an instance, following the documentation:
// https://www.tarantool.io/en/doc/latest/how-to/replication/repl_bootstrap/#disconnecting-an-instance
//
// It set up:
// instance.iproto.listen = {}
func patchCConfigExpel(config *libcluster.Config,
	inst cconfigInstance) (*libcluster.Config, error) {
	var (
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	if err := config.Set([]string{"groups", groupName, "replicasets", replicasetName,
		"instances", instName, "iproto", "listen"}, map[any]any{}); err != nil {
		return nil, err
	}
	return config, nil
}

// patchCConfigDemote patches the config to demote an instance.
func patchCConfigDemote(config *libcluster.Config,
	inst cconfigInstance) (*libcluster.Config, error) {
	var (
		failover       = inst.failover
		groupName      = inst.groupName
		replicasetName = inst.replicasetName
		instName       = inst.name
	)
	if failover != FailoverOff {
		return nil, fmt.Errorf("unexpected failover: %q", failover)
	}
	if err := config.Set([]string{"groups", groupName, "replicasets", replicasetName,
		"instances", instName, "database", "mode"}, "ro"); err != nil {
		return nil, err
	}
	return config, nil
}

// patchCConfigElectionMode patches the config to change an instance election_mode.
func patchCConfigElectionMode(config *libcluster.Config,
	inst cconfigInstance, mode ElectionMode) (*libcluster.Config, error) {
	path := []string{"groups", inst.groupName, "replicasets", inst.replicasetName,
		"instances", inst.name, "replication", "election_mode"}
	err := config.Set(path, mode.String())
	if err != nil {
		return nil, err
	}
	return config, nil
}

func patchCConfigEditRole(config *libcluster.Config,
	prt []patchRoleTarget) (*libcluster.Config, error) {
	for _, p := range prt {
		if err := config.Set(p.path, p.roleNames); err != nil {
			return nil, err
		}
	}
	return config, nil
}

// getCConfigInstance extracts an instance from the cluster config.
func getCConfigInstance(
	config *libcluster.ClusterConfig, instName string) (cconfigInstance, error) {
	var (
		inst  cconfigInstance
		found bool
		err   error
	)
	inst.name = instName
loop:
	for gname, group := range config.Groups {
		for rname, replicaset := range group.Replicasets {
			for iname := range replicaset.Instances {
				if instName == iname {
					inst.groupName = gname
					inst.replicasetName = rname
					found = true
					break loop
				}
			}
		}
	}
	if !found {
		return inst,
			fmt.Errorf("instance %q not found in the cluster configuration", instName)
	}
	inst.failover, err = cconfigGetFailover(config, instName)
	return inst, err
}
