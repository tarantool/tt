package replicaset

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/apex/log"
	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"

	cluster "github.com/tarantool/tt/cli/cluster"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

var (
	//go:embed lua/cconfig/get_instance_topology_body.lua
	cconfigGetInstanceTopologyBody string

	//go:embed lua/cconfig/promote_election.lua
	cconfigPromoteElectionBody string
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
	evaler connector.Evaler
}

// NewCConfigInstance create a new CConfigInstance object for the evaler.
func NewCConfigInstance(evaler connector.Evaler) *CConfigInstance {
	return &CConfigInstance{
		evaler: evaler,
	}
}

// Discovery returns a replicasets topology for a single instance with
// the centralized config orchestrator.
func (c *CConfigInstance) Discovery() (Replicasets, error) {
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

// CConfigApplication is an application with the centralized config
// orchestrator.
type CConfigApplication struct {
	runningCtx running.RunningCtx
	publishers libcluster.DataPublisherFactory
	collectors libcluster.DataCollectorFactory
}

// NewCConfigApplication creates a new CartridgeApplication object.
func NewCConfigApplication(
	runningCtx running.RunningCtx,
	collectors libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory) *CConfigApplication {
	return &CConfigApplication{
		runningCtx: runningCtx,
		publishers: publishers,
		collectors: collectors,
	}
}

// Discovery returns a replicasets topology for an application with
// the centralized config orchestrator.
func (c *CConfigApplication) Discovery() (Replicasets, error) {
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
	replicasets, err := c.Discovery()
	if err != nil {
		return fmt.Errorf("failed to get replicasets: %w", err)
	}

	var (
		targetReplicaset Replicaset
		targetInstance   Instance
		found            bool
	)
loop:
	for _, replicaset := range replicasets.Replicasets {
		for _, instance := range replicaset.Instances {
			if instance.Alias == ctx.InstName {
				targetReplicaset = replicaset
				targetInstance = instance
				found = true
				break loop
			}
		}
	}
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
			return fmt.Errorf("all instances in the target replicast should be online, %s", msg)
		}
		log.Warn(msg)
	}

	isConfigPatched, err := c.promote(targetInstance.InstanceCtx, ctx)
	if err != nil {
		return err
	}
	if isConfigPatched {
		err = reloadCConfig(instances)
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
// if the instance config was patched.
func (c *CConfigApplication) promote(instance running.InstanceCtx, ctx PromoteCtx) (bool, error) {
	clusterCfg, err := cluster.GetClusterConfig(libcluster.NewCollectorFactory(c.collectors),
		instance.ClusterConfigPath)
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
		err := EvalAny([]running.InstanceCtx{instance}, InstanceEvalFunc(eval))
		return false, err
	}

	collector, publisher, err := cconfigCreateCollectorAndDataPublisher(
		c.collectors,
		c.publishers,
		instance.ClusterConfigPath,
	)
	if err != nil {
		return false, err
	}

	config, err := collector.Collect()
	if err != nil {
		return false, fmt.Errorf("failed to collect a configuration to update: %w", err)
	}

	config, err = patchCConfigPromote(config, inst)
	if err != nil {
		return false, fmt.Errorf("failed to patch config: %w", err)
	}

	err = libcluster.NewYamlConfigPublisher(publisher).Publish(config)
	return true, err
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
