package replicaset

import (
	_ "embed"
	"fmt"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
)

//go:embed lua/cartridge/get_topology_replicasets_body.lua
var cartridgeGetTopologyReplicasetsBody string

//go:embed lua/cartridge/get_instance_info_body.lua
var cartridgeGetInstanceInfoBody string

// cartridgeTopology used to export topology information from a Tarantool
// instance with the Cartridge orchestrator.
type cartridgeTopology struct {
	// Failover is a string representation of a failover.
	Failover string
	// Provider is a string representation of a state provider.
	Provider string
	// Replicasets is an array of replicasets.
	Replicasets []Replicaset
	// IsCritical indicates whether instance has critical issues.
	IsCritical bool `mapstructure:"is_critical"`
	// IsBootstrapped indicates whether instance is bootstrapped.
	IsBootstrapped bool
}

// CartridgeInstance is an instance with the Cartridge orchestrator.
type CartridgeInstance struct {
	evaler connector.Evaler
}

// NewCartridgeInstance creates a new CartridgeInstance object for the evaler.
func NewCartridgeInstance(evaler connector.Evaler) *CartridgeInstance {
	return &CartridgeInstance{
		evaler: evaler,
	}
}

// getCartridgeTopology returns a cartridge topology received from an instance.
func getCartridgeTopology(evaler connector.Evaler) (cartridgeTopology, error) {
	var topology cartridgeTopology
	args := []any{}
	opts := connector.RequestOpts{}
	data, err := evaler.Eval(cartridgeGetTopologyReplicasetsBody, args, opts)
	if err != nil {
		return topology, err
	}

	if len(data) != 1 {
		return topology, fmt.Errorf("unexpected response: %v", data)
	}

	if err := mapstructure.Decode(data[0], &topology); err != nil {
		return topology, fmt.Errorf("failed to parse a response: %w", err)
	}

	topology.IsBootstrapped = len(topology.Replicasets) > 0
	return topology, nil
}

// getCartridgeReplicasets converts cartridgeTopology to Replicasets.
func getCartridgeReplicasets(topology cartridgeTopology) Replicasets {
	replicasets := Replicasets{
		State:        StateUninitialized,
		Replicasets:  topology.Replicasets,
		Orchestrator: OrchestratorCartridge,
	}
	if topology.IsBootstrapped {
		replicasets.State = StateBootstrapped
		failover := ParseFailover(topology.Failover)
		provider := ParseStateProvider(topology.Provider)
		for i := range replicasets.Replicasets {
			replicasets.Replicasets[i].Failover = failover
			replicasets.Replicasets[i].StateProvider = provider
		}
	}
	return replicasets
}

// GetReplicasets returns a replicaset topology for a single instance with the
// Cartridge orchestrator.
func (c *CartridgeInstance) GetReplicasets() (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateUnknown,
		Orchestrator: OrchestratorCartridge,
	}
	topology, err := getCartridgeTopology(c.evaler)
	if err != nil {
		return replicasets, err
	}

	replicasets = getCartridgeReplicasets(topology)
	if len(replicasets.Replicasets) > 0 {
		replicasets, err = updateCartridgeInstance(c.evaler, nil, replicasets)
		if err != nil {
			return replicasets, err
		}
	}
	return recalculateMasters(replicasets), nil
}

// CartridgeApplication is an application with the Cartridge orchestrator.
type CartridgeApplication struct {
	runningCtx running.RunningCtx
}

// NewCartridgeApplication creates a new CartridgeApplication object.
func NewCartridgeApplication(runningCtx running.RunningCtx) *CartridgeApplication {
	return &CartridgeApplication{
		runningCtx: runningCtx,
	}
}

// GetReplicasets returns a replicaset topology for an application with
// the Cartridge orchestrator.
func (c *CartridgeApplication) GetReplicasets() (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateUnknown,
		Orchestrator: OrchestratorCartridge,
	}

	var topology cartridgeTopology
	err := EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
		func(inst running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			newTopology, err := getCartridgeTopology(evaler)
			if err != nil {
				return true, err
			}
			if topology.IsBootstrapped {
				if newTopology.IsBootstrapped {
					topology = newTopology
				}
			} else {
				topology = newTopology
			}

			// Stop if we already found a valid topology.
			return topology.IsBootstrapped && !topology.IsCritical, nil
		},
	))
	if err != nil {
		return replicasets, fmt.Errorf("failed to get topology: %w", err)
	}

	replicasets = getCartridgeReplicasets(topology)
	err = EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
		func(ictx running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			var err error
			replicasets, err = updateCartridgeInstance(evaler, &ictx, replicasets)
			return false, err
		},
	))

	return recalculateMasters(replicasets), err
}

// updateCartridgeInstance receives and updates an additional instance
// information about the instance in the replicasets.
func updateCartridgeInstance(evaler connector.Evaler,
	ictx *running.InstanceCtx, replicasets Replicasets) (Replicasets, error) {
	info := []struct {
		UUID string
		RW   bool
	}{}

	args := []any{}
	opts := connector.RequestOpts{}
	data, err := evaler.Eval(cartridgeGetInstanceInfoBody, args, opts)
	if err != nil {
		return replicasets, err
	}

	if err := mapstructure.Decode(data, &info); err != nil {
		return replicasets, fmt.Errorf("failed to parse a response: %w", err)
	}
	if len(info) != 1 {
		return replicasets, fmt.Errorf("unexpected response")
	}

	for _, replicaset := range replicasets.Replicasets {
		for i, _ := range replicaset.Instances {
			if replicaset.Instances[i].UUID == info[0].UUID {
				if info[0].RW {
					replicaset.Instances[i].Mode = ModeRW
				} else {
					replicaset.Instances[i].Mode = ModeRead
				}
				if ictx != nil {
					replicaset.Instances[i].InstanceCtx = *ictx
					replicaset.Instances[i].InstanceCtxFound = true
				}
			}
		}
	}
	return replicasets, nil
}
