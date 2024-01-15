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

//go:embed lua/cartridge/edit_instances_body.lua
var cartridgeEditInstancesBody string

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

// Discovery returns a replicaset topology for a single instance with the
// Cartridge orchestrator.
func (c *CartridgeInstance) Discovery() (Replicasets, error) {
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

// Expel is not supported for a single instance by the Cartridge orchestrator.
func (c *CartridgeInstance) Expel(name string) error {
	return newErrExpelByInstanceNotSupported(OrchestratorCartridge)
}

// CartridgeApplication is an application with the Cartridge orchestrator.
type CartridgeApplication struct {
	runningCtx running.RunningCtx
	preferred  connector.Evaler
	// The cached result. There is no need to re-discovery a replicasets
	// for our application.
	cached      bool
	replicasets Replicasets
}

// NewCartridgeApplication creates a new CartridgeApplication object.
func NewCartridgeApplication(runningCtx running.RunningCtx,
	preferred connector.Evaler) *CartridgeApplication {
	return &CartridgeApplication{
		runningCtx: runningCtx,
		preferred:  preferred,
	}
}

// Discovery returns a replicaset topology for an application with
// the Cartridge orchestrator.
func (c *CartridgeApplication) Discovery() (Replicasets, error) {
	// Discovery() forces re-discovery.
	c.cached = false
	replicasets := Replicasets{
		State:        StateUnknown,
		Orchestrator: OrchestratorCartridge,
	}

	var err error
	if c.preferred != nil {
		replicasets, err = NewCartridgeInstance(c.preferred).Discovery()
	} else {
		err = EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
			func(inst running.InstanceCtx, evaler connector.Evaler) (bool, error) {
				var err error
				replicasets, err = NewCartridgeInstance(evaler).Discovery()
				if err == nil && replicasets.State == StateUninitialized {
					// Try again with another instance if the current is in
					// the uninitialized state.
					return false, nil
				}
				return true, err
			},
		))
	}
	if err != nil {
		return replicasets, fmt.Errorf("failed to get topology: %w", err)
	}
	if replicasets.State == StateUninitialized {
		// Nothing to do here.
		return replicasets, nil
	}

	err = EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
		func(ictx running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			var err error
			replicasets, err = updateCartridgeInstance(evaler, &ictx, replicasets)
			return false, err
		},
	))
	if err != nil {
		return replicasets, nil
	}

	c.replicasets = recalculateMasters(replicasets)
	c.cached = true
	return c.replicasets, nil
}

// Expel expels an instance from a Cartridge replicasets.
func (c *CartridgeApplication) Expel(name string) error {
	replicasets := c.replicasets
	if !c.cached {
		var err error
		if replicasets, err = c.Discovery(); err != nil {
			return fmt.Errorf("failed to discovery: %s", err)
		}
	}

	var (
		uuid  string
		found bool
	)
	for _, replicaset := range replicasets.Replicasets {
		for _, instance := range replicaset.Instances {
			if instance.Alias == name {
				uuid = instance.UUID
				found = true
				break
			}
		}
	}
	if !found {
		return fmt.Errorf("instance %q not found in a configured replicaset", name)
	}

	return cartridgeExpel(c.runningCtx, name, uuid)
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

// cartridgeExpel expels an instance from the replicaset.
func cartridgeExpel(runningCtx running.RunningCtx, name, uuid string) error {
	found := false
	var lastErr error
	eval := func(instance running.InstanceCtx, evaler connector.Evaler) (bool, error) {
		if instance.InstName == name {
			return false, nil
		}
		found = true

		args := []any{[]any{map[any]any{"uuid": uuid, "expelled": true}}}
		opts := connector.RequestOpts{}
		_, err := evaler.Eval(cartridgeEditInstancesBody, args, opts)
		if err != nil {
			// Try again with another instance.
			lastErr = err
			return false, nil
		}

		lastErr = nil
		return true, nil
	}

	err := EvalForeachAlive(runningCtx.Instances, InstanceEvalFunc(eval))
	for _, e := range []error{err, lastErr} {
		if e != nil {
			return fmt.Errorf("failed to expel instance: %w", e)
		}
	}

	if !found {
		return fmt.Errorf("not found any other instance joined to cluster")
	}

	return nil
}
