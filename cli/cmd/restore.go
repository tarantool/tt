package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/apex/log"
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/restore"
)

// tt restore apply flags. They are package-level because cobra flag bindings
// need stable addresses; only one restore subcommand runs per process.
var (
	restoreApplyArchives  []string
	restoreApplyChecksums []string
	restoreApplyWorkDir   string
	restoreApplyPoint     string
	restoreApplyPointName string
	restoreApplyPatchUUID string
)

const (
	// restoreApplyNoTrimFileExitCode is returned when no unpacked xlog covers
	// --target-point. The point and the archives disagree, which is a
	// different problem from a node that failed to unpack.
	restoreApplyNoTrimFileExitCode = 2

	// restoreApplyValidationExitCode is returned when an input is rejected.
	// The work directory is untouched in this case, so the orchestrator can
	// fix the call and retry without having destroyed the previous attempt.
	restoreApplyValidationExitCode = 3
)

// NewRestoreCmd creates the parent `tt restore` command.
func NewRestoreCmd() *cobra.Command {
	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a Tarantool cluster from a backup (PITR)",
	}

	restoreCmd.AddCommand(
		newRestoreApplyCmd(),
	)

	return restoreCmd
}

// restoreApplyLong is the help text of `tt restore apply`.
const restoreApplyLong = `Rebuild an instance's work directory from a backup chain so Tarantool can
start on a chosen recovery point.

Usage:
  tt restore apply --archives <full,inc1,inc2> --work-dir <path> \
      [--target-point '{"replica_id":N,"lsn":M}'] [--patch-uuid <uuid>]

Run once per instance, on the node itself, after the orchestrator has copied
the archives over and stopped the instance. Apply does not stop or start
Tarantool and does not talk to the backup storage.

The chain is unpacked in the order given, so --archives takes the full backup
first and then each increment, exactly as 'tt restore plan' lists them under
download_plan[<replicaset_uuid>]. --target-point is that plan's
recovery_point.trim_to_by_replicaset[<replicaset_uuid>].

--patch-uuid is required in practice. The archive was taken on the master and
is handed to every node of the replicaset, so without it the replicas come up
sharing the master's instance UUID. Take the new UUID by instance_name from
the manifest topology. Omit it only on a damaged instance whose target UUID
cannot be established; the headers then keep the UUID they carry.

Re-running for the same point is idempotent: apply clears what the previous
run left in the work directory and repeats the work. Cleanup removes only the
files a restore owns -- snapshots, xlogs, vinyl data and interrupted
leftovers -- so an instance config or log kept in the same directory survives.

On success a restore_state.json marker is written next to the work directory
(named after it, not inside it). Compare the markers of every node before
starting the cluster: a restore that silently skipped one node brings up
shards sitting on different states, each self-consistent and replicating
happily, which surfaces much later as diverged buckets.

Exit codes:
  0  the work directory is ready
  2  no xlog covers --target-point
  3  an input was rejected; the work directory was not touched
  1  unpacking, patching or trimming failed

Examples:
  tt restore apply --archives /opt/restore/full.tar.zst,/opt/restore/inc1.tar.zst \
      --work-dir /var/lib/tarantool/router-001 \
      --target-point '{"replica_id":1,"lsn":1502}' \
      --patch-uuid 550e8400-e29b-41d4-a716-446655440000`

// newRestoreApplyCmd creates `tt restore apply`.
func newRestoreApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Prepare an instance work directory from backup archives",
		Long:  restoreApplyLong,
		Args:  cobra.NoArgs,
		RunE:  runRestoreApply,
		// A failed run has already said what it managed to do; burying that
		// under the flag list is the last thing an operator needs here.
		SilenceUsage: true,
	}

	cmd.Flags().StringSliceVar(&restoreApplyArchives, "archives", nil,
		"ordered backup chain: the full backup first, then each increment")
	cmd.Flags().StringSliceVar(&restoreApplyChecksums, "checksums", nil,
		"sha256 of each archive, in the same order as --archives")
	cmd.Flags().StringVar(&restoreApplyWorkDir, "work-dir", "",
		"instance work directory to rebuild")
	cmd.Flags().StringVar(&restoreApplyPoint, "target-point", "",
		`recovery point position to cut the final xlog at, `+
			`as '{"replica_id":N,"lsn":M}'`)
	cmd.Flags().StringVar(&restoreApplyPointName, "point-name", "",
		"name of the cluster recovery point, recorded in restore_state.json")
	cmd.Flags().StringVar(&restoreApplyPatchUUID, "patch-uuid", "",
		"new instance UUID to stamp into every snap/xlog header")

	cmd.MarkFlagRequired("archives")
	cmd.MarkFlagRequired("work-dir")

	return cmd
}

func runRestoreApply(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	result, err := runRestoreApplyInner()
	if err != nil {
		switch {
		case errors.Is(err, restore.ErrNoTrimFile):
			log.Error(err.Error())
			os.Exit(restoreApplyNoTrimFileExitCode)
		case errors.Is(err, restore.ErrValidation):
			log.Error(err.Error())
			os.Exit(restoreApplyValidationExitCode)
		}

		return fmt.Errorf("restore apply: %w", err)
	}

	reportRestoreApply(result)

	return nil
}

// runRestoreApplyInner parses the flags and runs the restore.
func runRestoreApplyInner() (*restore.ApplyResult, error) {
	var (
		point *restore.Point
		err   error
	)

	if restoreApplyPoint != "" {
		if point, err = restore.ParsePoint(restoreApplyPoint); err != nil {
			return nil, err //nolint:wrapcheck
		}
	} else {
		// Loud, because the difference is invisible afterwards: the instance
		// comes up on the end of the chain instead of the chosen point, and
		// every shard restored this way is internally consistent.
		log.Warn("no --target-point given: replaying the whole chain, " +
			"nothing will be trimmed")
	}

	if restoreApplyPatchUUID == "" {
		log.Warn("no --patch-uuid given: keeping the instance UUID of the " +
			"backed-up master; correct only for a damaged instance whose " +
			"target UUID cannot be established")
	}

	if len(restoreApplyChecksums) == 0 {
		log.Warn("no --checksums given: the archives are taken on trust, " +
			"a copy to this node that went wrong will not be noticed")
	}

	return restore.Apply(restore.ApplyOpts{ //nolint:wrapcheck
		Archives:  restoreApplyArchives,
		Checksums: restoreApplyChecksums,
		WorkDir:   restoreApplyWorkDir,
		Point:     point,
		PointName: restoreApplyPointName,
		PatchUUID: restoreApplyPatchUUID,
	})
}

// reportRestoreApply prints what the run produced.
func reportRestoreApply(result *restore.ApplyResult) {
	log.Infof("unpacked %d file(s) into %s: %s",
		len(result.Files), restoreApplyWorkDir, strings.Join(result.Files, ", "))

	if result.Patched > 0 {
		log.Infof("stamped instance uuid %s into %d header(s)",
			restoreApplyPatchUUID, result.Patched)
	}

	if result.TrimmedFile != "" {
		log.Infof("trimmed %s at %s", result.TrimmedFile, restoreApplyPoint)
	}

	if len(result.DroppedFiles) > 0 {
		log.Infof("dropped %d file(s) starting past the recovery point: %s",
			len(result.DroppedFiles), strings.Join(result.DroppedFiles, ", "))
	}

	log.Infof("work directory ready, marker written to %s", result.StatePath)
}
