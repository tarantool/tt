package replicasetcmd

import (
	"fmt"
	"os"
	"time"

	"github.com/avast/retry-go"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/replicaset"
	"github.com/tarantool/tt/v3/cli/running"
)

const (
	// VShardBootstrapDefaultTimeout is a default timeout for vshard bootstrapping.
	VShardBootstrapDefaultTimeout = 10
)

// VShardCmdCtx describes context for vshard commands.
type VShardCmdCtx struct {
	// IsApplication true if an application passed.
	IsApplication bool
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Conn is an active connection to a passed instance.
	Conn connector.Connector
	// Orchestrator is the orchestrator to use.
	Orchestrator replicaset.Orchestrator
	// Publishers is data publisher factory.
	Publishers sdkcluster.Factory
	// Collectors is data collector factory.
	Collectors sdkcluster.Factory
	// Integrity is the integrity context for cluster reads.
	Integrity integrity.IntegrityCtx
	// Timeout describes a timeout in seconds.
	// We keep int as it can be passed to the target instance.
	Timeout int
}

func discoverApp(orchestrator replicasetOrchestrator) error {
	// Get and print status.
	replicasets, err := orchestrator.Discovery(true)
	if err != nil {
		return err
	}

	_ = statusReplicasets(replicasets)

	return nil
}

// BootstrapVShard bootstraps vshard in the cluster.
func BootstrapVShard(ctx VShardCmdCtx) error {
	var (
		orchestrator replicasetOrchestrator
		err          error
	)

	if ctx.IsApplication {
		orchestrator, err = makeApplicationOrchestrator(
			ctx.Orchestrator, ctx.RunningCtx, ctx.Collectors, ctx.Publishers, ctx.Integrity)
		if err != nil {
			return err
		}
	} else {
		orchestrator, err = makeInstanceOrchestrator(ctx.Orchestrator, ctx.Conn)
		if err != nil {
			return err
		}
	}

	log.Info("Discovery application...")

	_, _ = fmt.Fprintln(os.Stdout, "")

	retryOpts := []retry.Option{
		retry.Delay(1 * time.Second),
		retry.Attempts(uint(ctx.Timeout)),
		retry.LastErrorOnly(true),
	}
	discoverAppFunc := func() error {
		return discoverApp(orchestrator)
	}

	err = retry.Do(discoverAppFunc, retryOpts...)
	if err != nil {
		return fmt.Errorf("failed to bootstrap vshard: %w", err)
	}

	_, _ = fmt.Fprintln(os.Stdout, "")

	log.Info("Bootstrapping vshard")

	err = orchestrator.BootstrapVShard(replicaset.VShardBootstrapCtx{Timeout: ctx.Timeout})
	if err == nil {
		log.Info("Done.")
	}

	return err
}
