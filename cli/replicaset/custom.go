package replicaset

import (
	_ "embed"
	"fmt"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
)

//go:embed lua/custom/get_instance_topology_body.lua
var customGetInstanceTopologyBody string

// customTopology used to export topology information from a Tarantool instance
// with a custom orchestrator.
type customTopology struct {
	// UUID is a current replicaset UUID.
	UUID string
	// LeaderUUID is a leader UUID in the replicaset.
	LeaderUUID string
	// Alias is a short name of the replicaset.
	Alias string
	// Instances is a list of known instances in a replicaset.
	Instances []Instance
	// InstanceUUID is a current instance UUID.
	InstanceUUID string
	// InstanceRW is true when the current instance is in RW mode.
	InstanceRW bool
}

// CustomInstance is an instance with custom/unknown orchestrator. In this
// case, we can obtain a minimum of information for a replicaset.
type CustomInstance struct {
	evaler connector.Evaler
}

// NewCustomInstance creates a new CustomInstance object for the evaler.
func NewCustomInstance(evaler connector.Evaler) *CustomInstance {
	return &CustomInstance{
		evaler: evaler,
	}
}

// GetReplicasets returns a replicasets topology for a single
// instance with a custom type of orchestrator.
func (c *CustomInstance) GetReplicasets() (Replicasets, error) {
	topology, err := getCustomInstanceTopology("", c.evaler)
	if err != nil {
		return Replicasets{}, err
	}

	return recalculateMasters(Replicasets{
		State:        StateBootstrapped,
		Orchestrator: OrchestratorCustom,
		Replicasets: []Replicaset{
			Replicaset{
				UUID:       topology.UUID,
				LeaderUUID: topology.LeaderUUID,
				Alias:      topology.Alias,
				Instances:  topology.Instances,
			},
		},
	}), nil
}

// CustomApplication is an application with a custom orchestrator.
type CustomApplication struct {
	runningCtx running.RunningCtx
	conn       connector.Connector
}

// NewCustomApplication creates a new CustomApplication object.
func NewCustomApplication(runningCtx running.RunningCtx) *CustomApplication {
	return &CustomApplication{
		runningCtx: runningCtx,
	}
}

// GetReplicasets returns a replicasets configuration for an application with
// a custom orchestrator.
func (c *CustomApplication) GetReplicasets() (Replicasets, error) {
	var topologies []customTopology

	err := EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
		func(inst running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			topology, err := getCustomInstanceTopology(inst.InstName, evaler)
			if err != nil {
				return true, err
			}

			topologies = append(topologies, topology)
			return false, nil
		}))
	if err != nil {
		return Replicasets{}, err
	}

	return mergeCustomTopologies(topologies)
}

// getCustomInstanceTopology returns a topology for an instance.
func getCustomInstanceTopology(name string,
	evaler connector.Evaler) (customTopology, error) {
	var topology customTopology

	args := []any{}
	opts := connector.RequestOpts{}
	data, err := evaler.Eval(customGetInstanceTopologyBody, args, opts)
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
			if topology.Instances[i].Alias == "" {
				topology.Instances[i].Alias = name
			}
		}
	}

	return topology, nil
}

// mergeCustomTopologies merges a custom topologies per an instance into a
// Replicaset object.
func mergeCustomTopologies(topologies []customTopology) (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateBootstrapped,
		Orchestrator: OrchestratorCustom,
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
			updateCustomInstances(replicaset, topology)
		} else {
			replicasets.Replicasets = append(replicasets.Replicasets, Replicaset{
				UUID:       topology.UUID,
				LeaderUUID: topology.LeaderUUID,
				Alias:      topology.Alias,
				Instances:  topology.Instances,
			})
		}
	}

	return recalculateMasters(replicasets), nil
}

// updateCustomInstances updates a custom instance in the replicaset
// according to the instance topology.
func updateCustomInstances(replicaset *Replicaset, topology customTopology) {
	for _, tinstance := range topology.Instances {
		var instance *Instance
		for i, _ := range replicaset.Instances {
			if tinstance.UUID == replicaset.Instances[i].UUID {
				instance = &replicaset.Instances[i]
			}
		}
		if instance != nil {
			if instance.Alias == "" {
				instance.Alias = tinstance.Alias
			}
			if instance.URI == "" {
				instance.URI = tinstance.URI
			}
			if instance.Mode == ModeUnknown {
				instance.Mode = tinstance.Mode
			}
		} else {
			replicaset.Instances = append(replicaset.Instances, tinstance)
		}
	}
}
