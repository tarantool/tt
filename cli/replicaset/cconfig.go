package replicaset

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/integrity"
	"github.com/tarantool/tt/cli/running"
)

//go:embed lua/cconfig/get_instance_topology_body.lua
var cconfigGetInstanceTopologyBody string

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

// Expel is not supported for a single instance by the centralized config
// orchestrator.
func (c *CConfigInstance) Expel(name string) error {
	return newErrExpelByInstanceNotSupported(OrchestratorCentralizedConfig)
}

// CConfigApplication is an application with the centralized config
// orchestrator.
type CConfigApplication struct {
	runningCtx running.RunningCtx
	// The cached result. There is no need to re-discovery a replicasets
	// for our application.
	cached      bool
	replicasets Replicasets
}

// NewCConfigApplication creates a new CartridgeApplication object.
func NewCConfigApplication(runningCtx running.RunningCtx) *CConfigApplication {
	return &CConfigApplication{
		runningCtx: runningCtx,
	}
}

// Discovery returns a replicasets topology for an application with
// the centralized config orchestrator.
func (c *CConfigApplication) Discovery() (Replicasets, error) {
	// Discovery() forces re-discovery.
	c.cached = false

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

	replicasets, err := mergeCConfigTopologies(topologies)
	if err != nil {
		return replicasets, err
	}

	c.replicasets = replicasets
	c.cached = true
	return c.replicasets, nil
}

// Expel expels an instance from the cetralized config's replicasets.
func (c *CConfigApplication) Expel(name string) error {
	replicasets := c.replicasets
	if !c.cached {
		var err error
		if replicasets, err = c.Discovery(); err != nil {
			return fmt.Errorf("failed to discovery: %s", err)
		}
	}

	var (
		targetReplicaset Replicaset
		found            bool
	)
	for _, replicaset := range replicasets.Replicasets {
		for _, instance := range replicaset.Instances {
			if instance.Alias == name {
				targetReplicaset = replicaset
				found = true
				break
			}
		}
	}
	if !found {
		return fmt.Errorf("instance %q not found in a configured replicaset", name)
	}

	var targetInstance running.InstanceCtx
	found = false
	for _, instance := range c.runningCtx.Instances {
		if instance.InstName == name {
			targetInstance = instance
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("instance %q not found in the application", name)
	}

	return cconfigExpel(targetReplicaset, targetInstance)
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

// cconfigExpel expels an instance from the replicaset.
func cconfigExpel(replicaset Replicaset, instance running.InstanceCtx) error {
	if len(replicaset.Instances) == 1 {
		return fmt.Errorf("not found any other instance joined to a replicaset")
	}

	// Collect list of instances in the replicaset.
	instances := []running.InstanceCtx{}
	unfound := []string{}
	for _, inst := range replicaset.Instances {
		if !inst.InstanceCtxFound {
			// The target instance could be offline.
			if inst.Alias != instance.InstName {
				unfound = append(unfound, inst.Alias)
			}
		} else {
			instances = append(instances, inst.InstanceCtx)
		}
	}
	if len(unfound) > 0 {
		return fmt.Errorf("all instances in the target replicaset should "+
			"be online, could not connect to: %s", strings.Join(unfound, ", "))
	}

	// Update cluster configuration.
	if err := cconfigExpelUpdateConfig(instance); err != nil {
		return fmt.Errorf("failed to update cluster configuration: %w", err)
	}

	// Reload configuration on instances.
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
		return fmt.Errorf("failed to reload instances configuration for reqplicaset %q "+
			", please try to do it manually with `require('config'):reload()`: %w",
			replicaset.Alias, err)
	}

	if len(errored) > 0 {
		return fmt.Errorf("failed to reload instance configuration for: %s, "+
			"please try to do it manually with `require('config'):reload()`",
			strings.Join(errored, ", "))
	}
	return nil
}

// cconfigExpelUpdateConfig updates configuration for an instance with the name
// following the documentation:
// https://www.tarantool.io/en/doc/latest/how-to/replication/repl_bootstrap/#disconnecting-an-instance
//
// It set up:
// instance.iproto.listen = {}
func cconfigExpelUpdateConfig(instance running.InstanceCtx) error {
	// TODO: create integrity collectors factory from the command context if
	// needed instead of the global one.
	collectors, err := integrity.NewCollectorFactory()
	if err == integrity.ErrNotConfigured {
		collectors = cluster.NewCollectorFactory()
	} else if err != nil {
		return fmt.Errorf("failed to create collectors with integrity check: %w", err)
	}
	publishers := cluster.NewDataPublisherFactory()

	clusterCfg, err := cluster.GetClusterConfig(collectors, instance.ClusterConfigPath)
	if err != nil {
		return err
	}

	var (
		targetGroup      string
		targetReplicaset string
		found            bool
	)
	for gname, group := range clusterCfg.Groups {
		for rname, replicaset := range group.Replicasets {
			for iname, _ := range replicaset.Instances {
				if iname == instance.InstName {
					targetGroup = gname
					targetReplicaset = rname
					found = true
				}
			}
		}
	}
	if !found {
		// It is possible (configuration just updated).
		return fmt.Errorf("instance not found in the cluster configuration")
	}

	collector, err := collectors.NewFile(instance.ClusterConfigPath)
	if err != nil {
		return fmt.Errorf("failed to create a configuration collector: %w", err)
	}
	publisher, err := publishers.NewFile(instance.ClusterConfigPath)
	if err != nil {
		return fmt.Errorf("failed to create a configuration publisher: %w", err)
	}

	config, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to collect a configuration to update: %w", err)
	}
	err = config.Set([]string{"groups", targetGroup, "replicasets", targetReplicaset,
		"instances", instance.InstName, "iproto", "listen"}, map[any]any{})
	if err != nil {
		return fmt.Errorf("failed to update cluster configuration: %w", err)
	}

	err = cluster.NewYamlConfigPublisher(publisher).Publish(config)
	if err != nil {
		return fmt.Errorf("failed to publish cluster configuration: %w", err)
	}

	return nil
}
