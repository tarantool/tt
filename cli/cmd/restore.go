package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/apex/log"
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/restore"
)

// tt restore apply / plan flags. They are package-level because cobra flag
// bindings need stable addresses; only one restore subcommand runs per process.
var (
	restoreApplyArchives  []string
	restoreApplyChecksums []string
	restoreApplyWorkDir   string
	restoreApplyPoint     string
	restoreApplyPointName string
	restoreApplyPatchUUID string

	restorePlanTargetTime string
	restorePlanCfg        string
	restorePlanDir        string
	restorePlanFormat     string
	restorePlanTimeout    time.Duration
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

// Exit codes of `tt restore plan`. Each names one reason a restore cannot go
// ahead, because that is the orchestrator's branch point: the recovery
// procedure it runs next differs per reason, and a single failure code would
// send it to parse stderr to find out which one it hit.
const (
	restorePlanTopologyBoundaryExitCode = 2
	restorePlanNoRecoveryPointExitCode  = 3
	restorePlanChainBrokenExitCode      = 4
	restorePlanOutOfRangeExitCode       = 5
	restorePlanTopologyMismatchExitCode = 6
)

// NewRestoreCmd creates the parent `tt restore` command.
func NewRestoreCmd() *cobra.Command {
	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a Tarantool cluster from a backup (PITR)",
	}

	restoreCmd.AddCommand(
		newRestorePlanCmd(),
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

Run once per replicaset, on the node it is restored onto, after the
orchestrator has copied the archives over and stopped the instance. That node
is the instance the backup was taken on -- 'tt restore plan' names it in
restore_targets[<replicaset_uuid>].instance_name. The other members of the
replicaset are wiped instead and join it once the cluster is up, so apply is
not run on them. Apply does not stop or start Tarantool and does not talk to
the backup storage.

The chain is unpacked in the order given, so --archives takes the full backup
first and then each increment, exactly as 'tt restore plan' lists them under
download_plan[<replicaset_uuid>]. --target-point is that plan's
recovery_point.trim_to_by_replicaset[<replicaset_uuid>].

--patch-uuid takes restore_targets[<replicaset_uuid>].patch_uuid: the instance
UUID the restored node owned in the backed-up cluster, which is what its own
_cluster records and what the headers must say. Restoring onto the instance
the backup was taken on makes it the UUID already in the files, so the patch
confirms rather than changes them. Omitting the flag keeps whatever the
headers carry, which is right for that same case and wrong as soon as the
archives are replayed onto a differently named node.

Re-running for the same point is idempotent: apply clears what the previous
run left in the work directory and repeats the work. Cleanup removes only the
files a restore owns -- snapshots, xlogs, vinyl data and interrupted
leftovers -- so an instance config or log kept in the same directory survives.

On success a restore_state.json marker is written next to the work directory
(named after it, not inside it). Compare the markers of every restored node
before starting the cluster: a restore that silently skipped one replicaset
brings up shards sitting on different states, each self-consistent and
replicating happily, which surfaces much later as diverged buckets.

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
		// Right whenever the archives are replayed onto the instance they were
		// taken on, which is what `tt restore plan` points every replicaset at,
		// and wrong the moment they are not: the node would then claim another
		// instance's UUID.
		log.Warn("no --patch-uuid given: the headers keep the UUID they carry, " +
			"the one of the instance the backup was taken on")
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

// restorePlanLong is the help text of `tt restore plan`.
const restorePlanLong = `Work out how the cluster is brought back to a moment in time, and fetch
the backups that would do it.

Usage:
  tt restore plan --target-time <T> --backup-storage <config> -d <dir> \
      [--cluster-name <name> --environment <env>] \
      [-c <cluster config>] [--format table|json]

Run on the manager host, before anything is stopped. The command lists the
storage itself -- it is not handed a list of manifests -- walks the chain of
every backup back to the full it stands on, stitches the per-shard recovery
points into cluster-wide ones, and resolves --target-time into the latest
cluster point not later than it.

A cluster point is one whose label is present on every replicaset, so that
every shard comes back to the same moment. Points on either side of a
topology change are never stitched into one: a cluster whose composition or
master changed mid-window has no single state to return to.

The archives the chosen point needs are downloaded into --dir and their
checksums are verified there, so that a missing or corrupt archive stops the
plan while the cluster is still untouched. Distributing the files to the
nodes and applying them is the orchestrator's work and 'tt restore apply's.
Nothing is deleted, and the cluster being restored is never contacted.

With -c the point's topology is checked against the cluster the restore is
aimed at. The composition is read from the configuration, not from the
instances: this command's main scenario is a cluster whose nodes are dead or
crash-looping. Replicasets are matched by the instance names they share, never
by UUID, because the sanctioned target is a freshly deployed cluster where
every UUID is new and 'tt restore apply' stamps one into the restored node of
each replicaset. A replicaset
that has no counterpart blocks the plan; an instance the config no longer
carries is a warning.

Every replicaset is restored onto one node: the instance its backup was taken
on, whose headers the archives already fit. restore_targets names that node
per replicaset, together with the instance UUID it has to own afterwards --
what 'tt restore apply --patch-uuid' stamps in. With -c it also lists the
other configured members under rejoin: they are wiped before the restore and
come back by joining the restored node, and need no UUID of their own,
because Tarantool 3.x identifies an instance by name.

Exit codes:
  0  the plan is ready and the archives are downloaded
  2  the target time falls between two different topologies
  3  no cluster recovery point is available around the target time
  4  the chain is broken around the target time
  5  the target time is outside what the storage covers
  6  the point's topology does not match the cluster config
  1  the storage could not be read, or an archive is missing or corrupt

A failed plan carries nearest_safe wherever a usable point exists: the
recovery times on either side of the target, to offer and retry with. Code 6
also carries the difference that blocked it, manifest against config.

Examples:
  tt restore plan --target-time 2026-03-25T10:30:00Z \
      --backup-storage @s3-prod.yaml -d /tmp/restore/
  tt restore plan --target-time 1774435800 -c cluster.yaml \
      --backup-storage file:///var/backups -d /tmp/restore/ --format table
  tt restore plan --target-time 2026-03-25T10:30:00Z \
      --backup-storage file:///var/backups -d /tmp/restore/ \
      --cluster-name payments-cluster --environment production`

// newRestorePlanCmd creates `tt restore plan`.
func newRestorePlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Resolve a recovery time into a plan and download what it needs",
		Long:  restorePlanLong,
		Args:  cobra.NoArgs,
		RunE:  runRestorePlan,
		// A failed run has already said why in the plan it printed; burying
		// that under the flag list is the last thing an operator needs here.
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&restorePlanTargetTime, "target-time", "",
		"moment to recover to: RFC 3339 (2026-03-25T10:30:00Z) or a unix timestamp")
	addBackupStorageFlags(cmd)
	cmd.Flags().StringVarP(&restorePlanCfg, "config", "c", "",
		"cluster configuration of the cluster being restored, to check the "+
			"topology of the recovery point against.\n"+clusterUriHelp)
	cmd.Flags().StringVarP(&restorePlanDir, "dir", "d", "",
		"local directory to download the manifests and archives into")
	cmd.Flags().StringVar(&restorePlanFormat, "format", formatJSON,
		"output format: table or json")
	cmd.Flags().DurationVar(&restorePlanTimeout, "timeout", defaultWholeStorageTimeout,
		"timeout for reading from and downloading out of the storage; 0 means no limit")

	cmd.MarkFlagRequired("target-time")
	cmd.MarkFlagRequired("backup-storage")
	cmd.MarkFlagRequired("dir")

	return cmd
}

func runRestorePlan(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	result, err := runRestorePlanInner()
	if err != nil {
		return fmt.Errorf("restore plan: %w", err)
	}

	if code := restorePlanExitCode(result.Status); code != 0 {
		// The plan has already been printed and carries the reason; a second
		// error line would only repeat it. The code is what the orchestrator
		// branches on.
		os.Exit(code)
	}

	return nil
}

// runRestorePlanInner builds the plan, prints it, and returns it so that the
// caller can turn its status into an exit code.
func runRestorePlanInner() (*restore.PlanResult, error) {
	switch restorePlanFormat {
	case formatTable, formatJSON:
	default:
		return nil, fmt.Errorf("unsupported format %q: expected %q or %q",
			restorePlanFormat, formatTable, formatJSON)
	}

	targetTime, err := restore.ParseTargetTime(restorePlanTargetTime)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	store, err := openBackupStorage()
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	// Everything that can be rejected without touching the storage is rejected
	// first: a mistyped cluster config would otherwise surface after a whole
	// chain has been downloaded.
	var current *restore.ClusterTopology
	if restorePlanCfg != "" {
		if current, err = currentClusterTopology(); err != nil {
			return nil, err //nolint:wrapcheck
		}
	}

	ctx, cancel := storageContext(restorePlanTimeout)
	defer cancel()

	result, err := restore.Plan(ctx, restore.PlanOpts{
		Storage:    store,
		TargetTime: targetTime,
		Dir:        restorePlanDir,
		Current:    current,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	if err := printRestorePlan(result); err != nil {
		return nil, err //nolint:wrapcheck
	}

	return result, nil
}

// currentClusterTopology reads the composition of the cluster a restore is
// aimed at out of its configuration: replicaset names and the instance names in
// them, which is all the comparison needs and all a redeployed cluster has.
func currentClusterTopology() (*restore.ClusterTopology, error) {
	clusterConfig, _, err := loadTopologyConfig(&cmdCtx, restorePlanCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load the cluster config: %w", err)
	}

	configured := topologyFromConfig(clusterConfig)

	topology := restore.ClusterTopology{
		Replicasets: make([]restore.ConfiguredReplicaset, 0,
			len(configured.Replicasets)),
	}

	for _, replicaset := range configured.Replicasets {
		instances := make([]string, 0, len(replicaset.Instances))
		for _, instance := range replicaset.Instances {
			instances = append(instances, instance.Alias)
		}

		topology.Replicasets = append(topology.Replicasets,
			restore.ConfiguredReplicaset{Name: replicaset.Alias, Instances: instances})
	}

	if len(topology.Replicasets) == 0 {
		return nil, fmt.Errorf("cluster config %q declares no replicasets",
			restorePlanCfg)
	}

	return &topology, nil
}

// restorePlanExitCode maps a plan verdict to the code the orchestrator branches
// on. Zero means the plan is ready to be executed.
func restorePlanExitCode(status restore.Status) int {
	switch status {
	case restore.StatusOK:
		return 0
	case restore.StatusTopologyBoundary:
		return restorePlanTopologyBoundaryExitCode
	case restore.StatusNoRecoveryPoint:
		return restorePlanNoRecoveryPointExitCode
	case restore.StatusChainBroken:
		return restorePlanChainBrokenExitCode
	case restore.StatusOutOfRange:
		return restorePlanOutOfRangeExitCode
	case restore.StatusTopologyMismatch:
		return restorePlanTopologyMismatchExitCode
	default:
		// A status no code was assigned to must not read as success.
		return 1
	}
}

func printRestorePlan(result *restore.PlanResult) error {
	switch restorePlanFormat {
	case formatJSON:
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal the restore plan: %w", err)
		}

		fmt.Println(string(data))
	case formatTable:
		printRestorePlanTable(result)
	}

	return nil
}

// printRestorePlanTable prints the plan as a human-readable report.
func printRestorePlanTable(result *restore.PlanResult) {
	log.Info("Restore plan")
	log.Infof("  Status:        %s", result.Status)
	log.Infof("  Target time:   %s", result.TargetTime.Format(time.RFC3339))

	if result.Reason != "" {
		log.Warnf("  Reason:        %s", result.Reason)
	}

	if point := result.RecoveryPoint; point != nil {
		log.Infof("  Point:         %s at %s",
			point.Label, point.Timestamp.Format(time.RFC3339))
	}

	if safe := result.NearestSafe; safe != nil {
		if safe.Before != nil {
			log.Infof("  Nearest before: %s", safe.Before.Format(time.RFC3339))
		}
		if safe.After != nil {
			log.Infof("  Nearest after:  %s", safe.After.Format(time.RFC3339))
		}
	}

	if diff := result.TopologyDiff; diff != nil {
		printTopologyDiffTable(diff)
	}

	printDownloadPlanTable(result)
	printRestoreTargetsTable(result)

	for _, warning := range result.Warnings {
		log.Warnf("  %s", warning)
	}
}

// printRestoreTargetsTable names the node each replicaset is restored onto, the
// UUID it has to come up with, and the members that are wiped and rejoin it.
func printRestoreTargetsTable(result *restore.PlanResult) {
	if len(result.RestoreTargets) == 0 {
		return
	}

	log.Info("  Restore targets")

	// Ordered by uuid, like the download plan above it: the two sections are
	// read together, one replicaset at a time.
	for _, replicasetUUID := range slices.Sorted(maps.Keys(result.RestoreTargets)) {
		target := result.RestoreTargets[replicasetUUID]

		log.Infof("    %s", replicasetUUID)
		log.Infof("      restore onto  %s", target.InstanceName)

		if target.PatchUUID != "" {
			log.Infof("      --patch-uuid  %s", target.PatchUUID)
		}

		if len(target.Rejoin) > 0 {
			log.Infof("      wipe, rejoins %s", strings.Join(target.Rejoin, ", "))
		}
	}
}

// printDownloadPlanTable lists what each replicaset replays, in replay order.
func printDownloadPlanTable(result *restore.PlanResult) {
	if len(result.DownloadPlan) == 0 {
		return
	}

	log.Infof("  Downloaded:    %d replicaset(s)", len(result.DownloadPlan))

	// Ordered by uuid: map iteration order would otherwise reshuffle the report
	// between two runs of the same plan.
	for _, replicasetUUID := range slices.Sorted(maps.Keys(result.DownloadPlan)) {
		log.Infof("    %s", replicasetUUID)

		for _, item := range result.DownloadPlan[replicasetUUID] {
			log.Infof("      %-11s %s", item.Type, item.Artifact)
			if item.TrimTo != nil {
				log.Infof("                  trim to %s", item.TrimTo)
			}
		}
	}
}

// printTopologyDiffTable spells out what blocked the plan, side by side.
func printTopologyDiffTable(diff *restore.TopologyDiff) {
	log.Warn("  Topology differences")

	for _, replicaset := range diff.MissingReplicasets {
		log.Warnf("    backed-up replicaset %s (%s) has no counterpart in the config",
			replicaset.ReplicasetUUID, strings.Join(replicaset.Instances, ", "))
	}

	for _, replicaset := range diff.ExtraReplicasets {
		log.Warnf("    configured replicaset %s (%s) is not in the backup",
			replicaset.Name, strings.Join(replicaset.Instances, ", "))
	}

	for _, replicaset := range diff.AmbiguousReplicasets {
		log.Warnf("    %s matches more than one replicaset: %s",
			replicasetDiffLabel(replicaset), strings.Join(replicaset.Candidates, ", "))
	}

	for _, master := range diff.UncoveredMasters {
		log.Warnf("    replicaset %s: the backed-up master %s is not in the "+
			"configured replicaset %s",
			master.ReplicasetUUID, master.MasterInstance, master.Name)
	}
}

// replicasetDiffLabel names a replicaset by the side of the comparison it came
// from: the backup knows a uuid, the config a name.
func replicasetDiffLabel(replicaset restore.ReplicasetDiff) string {
	if replicaset.ReplicasetUUID != "" {
		return "backed-up replicaset " + replicaset.ReplicasetUUID
	}

	return "configured replicaset " + replicaset.Name
}
