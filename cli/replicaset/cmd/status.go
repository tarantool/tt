package replicasetcmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

// DiscoveryCtx contains information about replicaset discovery.
type DiscoveryCtx struct {
	// IsApplication true if an application passed.
	IsApplication bool
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Conn is an active connection to a passed instance.
	Conn connector.Connector
	// Orchestrator is a forced orchestrator choice.
	Orchestrator replicaset.Orchestrator
}

// getReplicasets discovers and returns the list of replicasets.
func getReplicasets(ctx DiscoveryCtx) (replicaset.Replicasets, error) {
	orchestratorType, err := getOrchestratorType(ctx.Orchestrator, ctx.Conn, ctx.RunningCtx)
	if err != nil {
		return replicaset.Replicasets{}, err
	}

	var orchestrator replicasetOrchestrator
	if ctx.IsApplication {
		orchestrator, err = makeApplicationOrchestrator(orchestratorType,
			ctx.RunningCtx, nil, nil)
	} else {
		orchestrator, err = makeInstanceOrchestrator(orchestratorType, ctx.Conn)
	}

	if err != nil {
		return replicaset.Replicasets{}, err
	}

	return orchestrator.Discovery(replicaset.SkipCache)
}

// Status shows a replicaset status.
func Status(discoveryCtx DiscoveryCtx) error {
	replicasets, err := getReplicasets(discoveryCtx)
	if err != nil {
		return err
	}

	return statusReplicasets(replicasets)
}

// statusReplicasets show the current status of known replicasets.
func statusReplicasets(replicasets replicaset.Replicasets) error {
	if replicasets.State == replicaset.StateUnknown {
		return fmt.Errorf("unknown or empty replicasets configuration")
	}

	fmt.Println("Orchestrator:     ", replicasets.Orchestrator)
	fmt.Println("Replicasets state:", replicasets.State)

	replicasets = fillAliases(replicasets)
	replicasets = sortAliases(replicasets)

	if len(replicasets.Replicasets) > 0 {
		fmt.Println()
	}
	for _, replicaset := range replicasets.Replicasets {
		fmt.Print(replicasetToString(replicaset))
	}
	return nil
}

// fillAliases fills missed aliases with UUID. The case: Tarantool 1.10 without
// an orchestrator.
func fillAliases(replicasets replicaset.Replicasets) replicaset.Replicasets {
	for i := range replicasets.Replicasets {
		replicaset := &replicasets.Replicasets[i]
		if replicaset.Alias == "" {
			replicaset.Alias = replicaset.UUID
		}

		for j := range replicaset.Instances {
			instance := &replicaset.Instances[j]
			if instance.Alias == "" {
				instance.Alias = instance.UUID
			}
		}
	}

	return replicasets
}

// sortAliases sorts replicasets and instances by an alias names.
func sortAliases(replicasets replicaset.Replicasets) replicaset.Replicasets {
	for _, replicaset := range replicasets.Replicasets {
		sort.Slice(replicaset.Instances, func(i, j int) bool {
			return replicaset.Instances[i].Alias < replicaset.Instances[j].Alias
		})
	}
	sort.Slice(replicasets.Replicasets, func(i, j int) bool {
		return replicasets.Replicasets[i].Alias < replicasets.Replicasets[j].Alias
	})
	return replicasets
}

// replicasetToString returns a string representation of a replicaset.
func replicasetToString(replicas replicaset.Replicaset) string {
	ret := "• " + replicas.Alias + "\n"
	ret += "  Failover: " + replicas.Failover.String() + "\n"
	if replicas.StateProvider != replicaset.StateProviderUnknown {
		ret += "  Provider: " + replicas.StateProvider.String() + "\n"
	}
	if replicas.Master != replicaset.MasterUnknown {
		ret += "  Master:   " + replicas.Master.String() + "\n"
	}
	if len(replicas.Roles) > 0 {
		ret += "  Roles:    " + strings.Join(replicas.Roles, ", ") + "\n"
	}
	for _, instance := range replicas.Instances {
		if replicas.LeaderUUID != "" && replicas.LeaderUUID == instance.UUID {
			ret += "    ★ "
		} else {
			ret += "    • "
		}
		ret += instanceToString(instance) + "\n"
	}
	return ret
}

// instanceToString returns a string representation of an instance.
func instanceToString(instance replicaset.Instance) string {
	return instance.Alias + " " + instance.URI + " " + instance.Mode.String()
}
