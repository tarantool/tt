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

// GetReplicasets returns a replicaset topology for a single instance with the
// Cartridge orchestrator.
func (c *CartridgeInstance) GetReplicasets() (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateUnknown,
		Orchestrator: OrchestratorCartridge,
	}
	args := []any{}
	opts := connector.RequestOpts{}
	data, err := c.evaler.Eval(cartridgeGetTopologyReplicasetsBody, args, opts)
	if err != nil {
		return replicasets, err
	}

	if len(data) != 1 {
		return replicasets, fmt.Errorf("unexpected response: %v", data)
	}

	var topology cartridgeTopology
	if err := mapstructure.Decode(data[0], &topology); err != nil {
		return replicasets, fmt.Errorf("failed to parse a response: %w", err)
	}

	replicasets.State = StateUninitialized
	replicasets.Replicasets = topology.Replicasets
	if len(replicasets.Replicasets) > 0 {
		replicasets.State = StateBootstrapped
		failover := ParseFailover(topology.Failover)
		provider := ParseStateProvider(topology.Provider)
		for i, _ := range replicasets.Replicasets {
			replicasets.Replicasets[i].Failover = failover
			replicasets.Replicasets[i].StateProvider = provider
		}
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
	preferred  connector.Evaler
}

// NewCartridgeApplication creates a new CartridgeApplication object.
func NewCartridgeApplication(runningCtx running.RunningCtx,
	preferred connector.Evaler) *CartridgeApplication {
	return &CartridgeApplication{
		runningCtx: runningCtx,
		preferred:  preferred,
	}
}

// GetReplicasets returns a replicaset topology for an application with
// the Cartridge orchestrator.
func (c *CartridgeApplication) GetReplicasets() (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateUnknown,
		Orchestrator: OrchestratorCartridge,
	}

	var err error
	if c.preferred != nil {
		replicasets, err = NewCartridgeInstance(c.preferred).GetReplicasets()
	} else {
		err = EvalAny(c.runningCtx.Instances, InstanceEvalFunc(
			func(_ running.InstanceCtx, evaler connector.Evaler) (bool, error) {
				var err error
				replicasets, err = NewCartridgeInstance(evaler).GetReplicasets()
				return true, err
			},
		))
	}
	if err != nil {
		return replicasets, fmt.Errorf("failed to get topology: %w", err)
	}

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
