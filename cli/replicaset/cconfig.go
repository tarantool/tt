package replicaset

import (
	_ "embed"
	"fmt"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/connector"
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

// CConfigApplication is an application with the centralized config
// orchestrator.
type CConfigApplication struct {
	runningCtx running.RunningCtx
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
