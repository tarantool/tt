package replicasetcmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

// RolesChangeCtx describes the context to add/remove role for
// provided config scope.
type RolesChangeCtx struct {
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
	// Orchestrator is a forced orchestrator choice.
	Orchestrator replicaset.Orchestrator
	// Conn is an active connection to a passed instance.
	Conn connector.Connector
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Force is true if unavailable instances can be skipped.
	Force bool
	// Timeout describes a timeout in seconds.
	// We keep int as it can be passed to the target instance.
	Timeout int
}

// RolesChange adds/removes role with provided path target to config.
func RolesChange(ctx RolesChangeCtx, changeRoleAction replicaset.RolesChangerAction) error {
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

	action := []string{"Add", "to"}
	if changeRoleAction.Action() == replicaset.RemoveAction {
		action = []string{"Remove", "from"}
	}

	if ctx.IsGlobal {
		if orchestratorType == replicaset.OrchestratorCartridge {
			return fmt.Errorf("cannot pass --global (-G) flag due to cluster with cartridge")
		} else {
			log.Infof("%s role %s %s global scope")
		}
	}
	if ctx.GroupName != "" {
		if orchestratorType == replicaset.OrchestratorCartridge &&
			changeRoleAction.Action() == replicaset.RemoveAction {

			return fmt.Errorf("cannot provide vshard-group by removing role")
		}
		log.Infof("%s role %s %s group: %s", action[0], ctx.RoleName, action[1], ctx.GroupName)
	}
	if ctx.InstName != "" {
		if orchestratorType == replicaset.OrchestratorCartridge {
			return fmt.Errorf("cannot pass the instance or --instance (-i) flag due to cluster" +
				" with cartridge orchestrator can't add/remove role for instance scope")
		} else {
			log.Infof("%s role %s %s instance: %s", action[0], ctx.RoleName,
				action[1], ctx.InstName)
		}
	}
	if ctx.ReplicasetName != "" {
		log.Infof("%s role %s %s replicaset: %s", action[0], ctx.RoleName,
			action[1], ctx.ReplicasetName)
	}

	err = orchestrator.RolesChange(replicaset.RolesChangeCtx{
		InstName:       ctx.InstName,
		GroupName:      ctx.GroupName,
		ReplicasetName: ctx.ReplicasetName,
		IsGlobal:       ctx.IsGlobal,
		RoleName:       ctx.RoleName,
		Force:          ctx.Force,
		Timeout:        ctx.Timeout,
	}, changeRoleAction)
	if err == nil {
		log.Info("Done.")
	}
	return err
}
