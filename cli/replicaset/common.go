package replicaset

import (
	_ "embed"
	"fmt"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
)

var (
	//go:embed lua/wait_rw.lua
	waitRWBody string

	//go:embed lua/wait_ro.lua
	waitROBody string
)

// waitRW waits until the instance becomes rw.
func waitRW(eval connector.Evaler, timeout int) error {
	var opts connector.RequestOpts
	args := []any{timeout}
	_, err := eval.Eval(waitRWBody, args, opts)
	if err != nil {
		return fmt.Errorf("failed to wait rw: %w", err)
	}
	return nil
}

// waitRO waits until the instance becomes ro.
func waitRO(eval connector.Evaler, timeout int) error {
	var opts connector.RequestOpts
	args := []any{timeout}
	_, err := eval.Eval(waitROBody, args, opts)
	if err != nil {
		return fmt.Errorf("failed to wait ro: %w", err)
	}
	return nil
}

// filterDiscovered filters only discovered instances from the instances bunch.
func filterDiscovered(instances []running.InstanceCtx,
	discovered Replicasets,
) []running.InstanceCtx {
	discoveredMap := map[string]struct{}{}
	for _, replicaset := range discovered.Replicasets {
		for _, instance := range replicaset.Instances {
			discoveredMap[instance.Alias] = struct{}{}
		}
	}
	return filterInstances(instances, func(instance running.InstanceCtx) bool {
		_, ok := discoveredMap[instance.InstName]
		return ok
	})
}

// filterInstances filter instances with the predicate.
func filterInstances(instances []running.InstanceCtx,
	filter func(running.InstanceCtx) bool,
) []running.InstanceCtx {
	var filtered []running.InstanceCtx
	for _, instance := range instances {
		if filter(instance) {
			filtered = append(filtered, instance)
		}
	}
	return filtered
}
