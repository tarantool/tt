package replicasetcmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

// RolesAddCtx describes the context to add a role to
// provided config scope.
type RolesAddCtx struct {
	// InstName is an instance name in which add or remove role.
	InstName string
	// GroupName is a replicaset name in which add or remove role.
	GroupName string
	// ReplicasetName is a replicaset name in which add or remove role.
	ReplicasetName string
	// IsGlobal is a boolean value if role needs to add in global scope.
	IsGlobal bool
	// RoleName is a name of role to add.
	RoleName string
	// Publishers is a data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is a data collector factory.
	Collectors libcluster.DataCollectorFactory
	// IsApplication is true if an application passed.
	IsApplication bool
	// Orchestrator is a forced orchestator choice.
	Orchestrator replicaset.Orchestrator
	// Conn is an active connection to a passed instance.
	Conn connector.Connector
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Force is true if unfound instances can be skipped.
	Force bool
	// Timeout describes a timeout in seconds.
	// We keep int as it can be passed to the target instance.
	Timeout int
}

// RolesAdd adds role with provided path target to config.
func RolesAdd(ctx RolesAddCtx) error {
	orchestratorType, err := getInstanceOrchestrator(ctx.Orchestrator, ctx.Conn)
	if err != nil {
		return err
	}

	if orchestratorType == replicaset.OrchestratorCartridge {
		if ctx.ReplicasetName == "" {
			return fmt.Errorf(
				"in cartridge replicaset name must be specified via --replicaset flag")
		}
	}

	var orchestrator replicasetOrchestrator
	if ctx.IsApplication {
		if orchestrator, err = makeApplicationOrchestrator(
			orchestratorType, ctx.RunningCtx, ctx.Collectors, ctx.Publishers); err != nil {
			return err
		}
	} else {
		if orchestrator, err = makeInstanceOrchestrator(orchestratorType, ctx.Conn); err != nil {
			return err
		}
	}

	log.Info("Discovery application...")
	fmt.Println()

	// Get and print status.
	replicasets, err := orchestrator.Discovery(replicaset.SkipCache)
	if err != nil {
		return err
	}
	statusReplicasets(replicasets)
	fmt.Println()

	if ctx.IsGlobal {
		if orchestratorType == replicaset.OrchestratorCartridge {
			return fmt.Errorf("cannot pass --global (-G) flag due to cluster with cartridge")
		} else {
			log.Infof("Add role %s to global scope", ctx.RoleName)
		}
	}
	if ctx.GroupName != "" && orchestratorType != replicaset.OrchestratorCartridge {
		log.Infof("Add role %s to group: %s", ctx.RoleName, ctx.GroupName)
	}
	if ctx.InstName != "" {
		if orchestratorType == replicaset.OrchestratorCartridge {
			return fmt.Errorf("cannot pass the instance or --instance (-i) flag due to cluster" +
				" with cartridge orchestrator can't add role into instance scope")
		} else {
			log.Infof("Add role %s to instance: %s", ctx.RoleName, ctx.InstName)
		}
	}
	if ctx.ReplicasetName != "" {
		log.Infof("Add role %s to replicaset: %s", ctx.RoleName, ctx.ReplicasetName)
	}

	err = orchestrator.RolesAdd(replicaset.RolesChangeCtx{
		InstName:       ctx.InstName,
		GroupName:      ctx.GroupName,
		ReplicasetName: ctx.ReplicasetName,
		IsGlobal:       ctx.IsGlobal,
		RoleName:       ctx.RoleName,
		Force:          ctx.Force,
		Timeout:        ctx.Timeout,
	})
	if err == nil {
		log.Info("Done.")
	}
	return err
}
