package restore

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

// Status is the verdict of a planning run. Five of the six come from resolving
// the target time against the chain; topology_mismatch is this command's own,
// because the chain is pure over manifests and never sees the cluster a restore
// is aimed at.
type Status string

const (
	StatusOK               Status = "ok"
	StatusTopologyBoundary Status = "topology_boundary"
	StatusNoRecoveryPoint  Status = "no_recovery_point"
	StatusChainBroken      Status = "chain_broken"
	StatusOutOfRange       Status = "out_of_range"
	StatusTopologyMismatch Status = "topology_mismatch"
)

// PlanOpts are the parameters of tt restore plan.
type PlanOpts struct {
	// Storage is the opened backup storage; the plan lists it itself rather
	// than being handed a set of manifests.
	Storage storage.Storage
	// TargetTime is the moment the cluster is to be brought back to.
	TargetTime time.Time
	// Dir is the local directory the manifests and archives land in.
	Dir string
	// Current is the cluster the restore is aimed at. Nil skips the topology
	// check and says so in the warnings: the check is worth having, but the
	// storage is worth reading without a cluster config at hand.
	Current *ClusterTopology
}

// PlanResult is the plan as it is printed. Everything an operator needs to see
// why a restore is or is not possible survives into it, so that a caller reads
// one document instead of an exit code plus a log.
type PlanResult struct {
	// Status decides the exit code and whether the archives were downloaded.
	Status Status `json:"status"`
	// TargetTime echoes the requested moment.
	TargetTime time.Time `json:"target_time"`
	// Reason names what stopped the plan; empty on success.
	Reason string `json:"reason,omitempty"`
	// RecoveryPoint is the cluster point the restore is built around.
	RecoveryPoint *RecoveryPoint `json:"recovery_point,omitempty"`
	// DownloadPlan maps a replicaset UUID to its ordered backup chain.
	DownloadPlan map[string][]DownloadItem `json:"download_plan,omitempty"`
	// RestoreTargets maps a replicaset UUID to the node its chain is restored
	// onto. Keyed like DownloadPlan, so one key answers both what a node
	// replays and what UUID it must own afterwards.
	RestoreTargets map[string]RestoreTarget `json:"restore_targets,omitempty"`
	// NearestSafe are the recovery times on either side of an unreachable
	// target, so that a caller can offer them and retry.
	NearestSafe *NearestSafe `json:"nearest_safe,omitempty"`
	// TopologyDiff explains a topology_mismatch.
	TopologyDiff *TopologyDiff `json:"topology_diff,omitempty"`
	// Warnings are what a restore survives but an operator should know.
	Warnings []string `json:"warnings"`
}

// RecoveryPoint is the chosen cluster point and its per-replicaset positions.
type RecoveryPoint struct {
	// Label is the point's cluster-wide label, what the point manager wrote on
	// every shard and what `tt restore apply --point-name` records.
	Label string `json:"label"`
	// Timestamp is the point's effective time: the earliest among its shards.
	Timestamp time.Time `json:"timestamp"`
	// TrimToByReplicaset is where each replicaset's journal is cut, keyed by
	// replicaset UUID. It is what `tt restore apply --target-point` takes.
	TrimToByReplicaset map[string]Point `json:"trim_to_by_replicaset"`
}

// DownloadItem is one archive of one replicaset's chain, in replay order.
type DownloadItem struct {
	BackupID string            `json:"backup_id"`
	Type     backup.BackupType `json:"type"`
	// Artifact is the local file, not the storage key: this is the path the
	// orchestrator copies to the node.
	Artifact string `json:"artifact"`
	// ChecksumSHA256 is the sum of the bytes that were downloaded. It travels
	// with the archive so that `tt restore apply --checksums` can re-check it
	// on the node, after a copy this command knows nothing about.
	ChecksumSHA256 string `json:"checksum_sha256"`
	// TrimXlog marks the archive holding the recovery point: everything the
	// journal carries past the point must not be replayed.
	TrimXlog bool `json:"trim_xlog,omitempty"`
	// TrimTo is where that archive's journal is cut.
	TrimTo *Point `json:"trim_to,omitempty"`
}

// RestoreTarget names the node one replicaset's chain is restored onto, and the
// instance UUID that node has to own once it is.
//
// A replicaset is backed up on its master, so the archive holds the master's
// files and the master is the node whose headers already fit them. Every other
// member is wiped and comes back by joining the restored node: Tarantool 3.x
// identifies an instance by name, so a member that boots empty reclaims its
// _cluster record under a fresh UUID and needs none of its own.
type RestoreTarget struct {
	// InstanceName is the node this replicaset's chain goes to.
	InstanceName string `json:"instance_name"`
	// PatchUUID is the instance UUID the restored node must carry: the one it
	// held in the backed-up cluster, which is what `tt restore apply
	// --patch-uuid` stamps into every header. Restoring onto the instance the
	// backup was taken on makes it the UUID already in the files, and the patch
	// a confirmation. It is absent only when the manifest recorded no UUID at
	// all, and then a warning says so rather than an empty value going out as
	// if it meant "do not patch".
	PatchUUID string `json:"patch_uuid,omitempty"`
	// Rejoin are the other configured instances of the replicaset: wiped before
	// the restore, joining the target once it is up. Filled only when a cluster
	// config was given, because the backup does not know the composition of the
	// cluster being restored into.
	Rejoin []string `json:"rejoin,omitempty"`
}

// NearestSafe are the recovery times bracketing an unreachable target. They are
// times rather than points because that is what goes back into --target-time.
type NearestSafe struct {
	Before *time.Time `json:"before,omitempty"`
	After  *time.Time `json:"after,omitempty"`
}

// noTopologyCheckWarning is what a run without a cluster config gives up.
const noTopologyCheckWarning = "no cluster config given (-c): the topology of the " +
	"recovery point was not checked against the cluster being restored"

// refuseTopology fills result for a point the cluster being restored does not
// match. The restore targets are computed by then and deliberately dropped: a
// plan that cannot go ahead must not hand out a node to restore onto, or an
// orchestrator reading the document by key would act on half of a refusal.
func refuseTopology(
	result *PlanResult,
	diff *TopologyDiff,
	backupChain *chain.Chain,
	opts PlanOpts,
) {
	result.Status = StatusTopologyMismatch
	result.Reason = reasonFor(StatusTopologyMismatch)
	result.TopologyDiff = diff

	// An alternative point is found by the same replicaset comparison that
	// rejected this one, so it can only answer a replicaset-level difference. A
	// master the config does not carry is the same master on every point of the
	// segment, and offering a neighbouring point would send the operator to one
	// that fails for the identical reason.
	if diff.replicasetLevel() {
		result.NearestSafe = nearestMatching(backupChain, *opts.Current,
			opts.TargetTime)
	}
}

// Plan resolves a target time against the manifests in the storage, checks the
// resulting point against the cluster it is to be restored into, downloads the
// archives the point needs into Dir and returns the plan built around them.
//
// A status other than StatusOK is a result, not an error: it says the restore
// cannot go ahead and why, and nothing is downloaded for it. An error means the
// storage could not be read at all, or an archive the plan needs is missing or
// corrupt - in which case there is no plan to hand out, and the cluster being
// restored has still not been touched.
func Plan(ctx context.Context, opts PlanOpts) (*PlanResult, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve --dir %q: %w", opts.Dir, err)
	}

	backupChain, err := chain.Load(ctx, opts.Storage)
	if err != nil {
		return nil, fmt.Errorf("failed to build the backup chain: %w", err)
	}

	result := &PlanResult{
		TargetTime: opts.TargetTime.UTC(),
		Warnings:   make([]string, 0),
	}

	resolution := backupChain.Resolve(opts.TargetTime)
	if resolution.Status != chain.StatusOK {
		result.Status = Status(resolution.Status.String())
		result.Reason = reasonFor(result.Status)
		result.NearestSafe = bracket(timestampOf(resolution.Before),
			timestampOf(resolution.After))

		return result, nil
	}

	point := *resolution.Point

	recoveryPlan, err := backupChain.PlanFor(point)
	if err != nil {
		return nil, fmt.Errorf("failed to build the recovery plan: %w", err)
	}

	topology := checkTopology(point, recoveryPlan, opts.Current)
	result.Warnings = append(result.Warnings, topology.warnings...)

	if diff := topology.diff; diff != nil {
		refuseTopology(result, diff, backupChain, opts)

		return result, nil
	}

	downloads, downloadWarnings, err := download(ctx, opts.Storage, dir, recoveryPlan)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	result.Warnings = append(result.Warnings, downloadWarnings...)
	result.Status = StatusOK
	result.RecoveryPoint = &RecoveryPoint{
		Label:              point.Name,
		Timestamp:          point.Timestamp,
		TrimToByReplicaset: trimToByReplicaset(point),
	}
	result.DownloadPlan = downloads
	result.RestoreTargets = topology.targets

	return result, nil
}

// ParseTargetTime decodes --target-time: an RFC 3339 timestamp, or a unix
// timestamp in seconds.
func ParseTargetTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}

	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), nil
	}

	return time.Time{}, fmt.Errorf(
		"--target-time %q is neither an RFC 3339 timestamp (2026-03-25T10:30:00Z) "+
			"nor a unix timestamp", raw)
}

// reasonFor explains a status in the one line the caller shows a human.
func reasonFor(status Status) string {
	switch status {
	case StatusTopologyBoundary:
		return "target time falls between manifests with different topology"
	case StatusNoRecoveryPoint:
		return "no cluster recovery point is available around the target time"
	case StatusChainBroken:
		return "the backup chain is broken around the target time"
	case StatusOutOfRange:
		return "target time is outside the coverage of the stored manifests"
	case StatusTopologyMismatch:
		return "the topology of the recovery point does not match the cluster config"
	default:
		return ""
	}
}

// planTopology is what comparing a point against the cluster being restored
// produced: where each replicaset's chain goes, what blocks the plan, and what
// an operator should know but a restore survives.
type planTopology struct {
	// targets name the node each replicaset is restored onto.
	targets map[string]RestoreTarget
	// diff is non-nil exactly when the plan cannot go ahead.
	diff *TopologyDiff
	// warnings never block.
	warnings []string
}

// checkTopology compares the point's topology with the cluster the restore is
// aimed at, and names the node each replicaset's chain goes to. Without a
// cluster config only the naming is possible: the target comes from the backup,
// the list of nodes to wipe from the configuration.
func checkTopology(
	point chain.ClusterPoint,
	plan chain.Plan,
	current *ClusterTopology,
) planTopology {
	instances, warnings := planInstances(plan)

	if current == nil {
		targets, unrecorded := restoreTargets(instances, nil)

		return planTopology{
			targets: targets,
			warnings: append(append([]string{noTopologyCheckWarning}, warnings...),
				unrecorded...),
		}
	}

	match := matchTopology(point.Topology, *current)
	match.diff.UncoveredMasters = uncoveredMasters(masterNames(instances), match.matched)

	targets, unrecorded := restoreTargets(instances, match.matched)

	result := planTopology{
		targets:  targets,
		warnings: append(append(match.warnings, warnings...), unrecorded...),
	}

	if !match.diff.Empty() {
		result.diff = &match.diff
	}

	return result
}

// planInstances returns the instance each replicaset's archives were taken on.
// It is the last backup of the chain that decides, because that is the manifest
// the point itself was found in: a chain can span a master change, and then the
// earlier backups of it name the instance that used to hold the role rather
// than the one whose archive the restore lands on.
func planInstances(plan chain.Plan) (map[string]*backup.ShardInstance, []string) {
	instances := make(map[string]*backup.ShardInstance, len(plan.Shards))
	unknown := make([]string, 0)

	for _, replicasetUUID := range slices.Sorted(maps.Keys(plan.Shards)) {
		backups := plan.Shards[replicasetUUID].Backups
		if len(backups) == 0 {
			continue
		}

		instance := backups[len(backups)-1].Shards[replicasetUUID].Instance
		if instance == nil || instance.InstanceName == "" {
			unknown = append(unknown, fmt.Sprintf(
				"replicaset %s: the backup does not name the instance it was taken "+
					"on, so the config cannot be checked for it", replicasetUUID))

			continue
		}

		instances[replicasetUUID] = instance
	}

	return instances, unknown
}

// restoreTargets names, per replicaset, the node its chain is restored onto and
// the UUID that node carries afterwards, plus the members that are wiped and
// rejoin it. The second result warns about a manifest that recorded no UUID for
// its own master, which is the one case the target cannot be completed from.
func restoreTargets(
	instances map[string]*backup.ShardInstance,
	matched map[string]ConfiguredReplicaset,
) (map[string]RestoreTarget, []string) {
	targets := make(map[string]RestoreTarget, len(instances))
	unrecorded := make([]string, 0)

	for _, replicasetUUID := range slices.Sorted(maps.Keys(instances)) {
		instance := instances[replicasetUUID]

		target := RestoreTarget{
			InstanceName: instance.InstanceName,
			PatchUUID:    instance.InstanceUUID,
		}

		if instance.InstanceUUID == "" {
			unrecorded = append(unrecorded, fmt.Sprintf(
				"replicaset %s: the backup records no instance uuid for %s, so "+
					"the plan carries no patch_uuid; restore onto that same "+
					"instance and omit --patch-uuid, whose headers already "+
					"carry it",
				replicasetUUID, instance.InstanceName))
		}

		if replicaset, ok := matched[replicasetUUID]; ok {
			target.Rejoin = missing(replicaset.Instances,
				[]string{instance.InstanceName})
		}

		targets[replicasetUUID] = target
	}

	return targets, unrecorded
}

// masterNames reduces the backed-up instances to the names the topology
// comparison works on.
func masterNames(instances map[string]*backup.ShardInstance) map[string]string {
	names := make(map[string]string, len(instances))
	for replicasetUUID, instance := range instances {
		names[replicasetUUID] = instance.InstanceName
	}

	return names
}

// nearestMatching offers the closest points on either side of the target whose
// topology does map onto the current configuration. It weighs replicasets only,
// the same comparison that blocked the plan; whether the offered point also
// covers its masters is decided when it is planned for.
func nearestMatching(
	backupChain *chain.Chain,
	current ClusterTopology,
	target time.Time,
) *NearestSafe {
	var before, after *time.Time

	for _, point := range backupChain.ClusterPoints() {
		if matchTopology(point.Topology, current).diff.replicasetLevel() {
			continue
		}

		timestamp := point.Timestamp
		if timestamp.After(target) {
			after = &timestamp
			break
		}

		before = &timestamp
	}

	return bracket(before, after)
}

// bracket builds the nearest-safe hint, or nothing when neither side exists.
func bracket(before, after *time.Time) *NearestSafe {
	if before == nil && after == nil {
		return nil
	}

	return &NearestSafe{Before: before, After: after}
}

// timestampOf returns a point's effective time, or nil for a missing point.
func timestampOf(point *chain.ClusterPoint) *time.Time {
	if point == nil {
		return nil
	}

	timestamp := point.Timestamp

	return &timestamp
}

// trimToByReplicaset renders the point's per-replicaset positions in the shape
// `tt restore apply --target-point` reads, so that the two agree by
// construction rather than by a conversion an operator has to make.
func trimToByReplicaset(point chain.ClusterPoint) map[string]Point {
	positions := make(map[string]Point, len(point.Shards))
	for replicasetUUID, position := range point.Shards {
		positions[replicasetUUID] = Point{
			ReplicaID: position.ReplicaID,
			LSN:       position.LSN,
		}
	}

	return positions
}

// download fetches every manifest and archive the plan needs into dir and
// returns the download plan built from what landed there.
func download(
	ctx context.Context,
	store storage.Storage,
	dir string,
	plan chain.Plan,
) (map[string][]DownloadItem, []string, error) {
	if err := os.MkdirAll(dir, downloadDirPerm); err != nil {
		return nil, nil, fmt.Errorf("failed to create %q: %w", dir, err)
	}

	claimed := make(destinations)

	// Manifests first: they are small, and a chain whose metadata cannot be
	// fetched is not worth spending an archive download on.
	for _, backupID := range planBackupIDs(plan) {
		key := storage.ManifestKey(string(backupID))

		destination, err := claimed.claim(dir, key)
		if err != nil {
			return nil, nil, err //nolint:wrapcheck
		}

		if _, err := fetch(ctx, store, key, destination, ""); err != nil {
			return nil, nil, fmt.Errorf(
				"failed to download the manifest of backup %s: %w", backupID, err)
		}
	}

	downloads := make(map[string][]DownloadItem, len(plan.Shards))
	warnings := make([]string, 0)

	for _, replicasetUUID := range slices.Sorted(maps.Keys(plan.Shards)) {
		shard := plan.Shards[replicasetUUID]
		items := make([]DownloadItem, 0, len(shard.Backups))

		for _, manifest := range shard.Backups {
			item, unverified, err := downloadArchive(
				ctx, store, dir, claimed, manifest, replicasetUUID)
			if err != nil {
				return nil, nil, err //nolint:wrapcheck
			}

			if unverified != "" {
				warnings = append(warnings, unverified)
			}

			items = append(items, item)
		}

		// The recovery point sits inside the last archive of the chain; a nil
		// TrimTo means it sits exactly at its end, and then there is nothing to
		// cut off.
		if last := len(items) - 1; last >= 0 && shard.TrimTo != nil {
			items[last].TrimXlog = true
			items[last].TrimTo = &Point{
				ReplicaID: shard.TrimTo.ReplicaID,
				LSN:       shard.TrimTo.LSN,
			}
		}

		downloads[replicasetUUID] = items
	}

	return downloads, warnings, nil
}

// downloadArchive fetches one replicaset's archive of one backup. The second
// result is a warning about a manifest that recorded no checksum, in which case
// the download could not be verified against anything.
func downloadArchive(
	ctx context.Context,
	store storage.Storage,
	dir string,
	claimed destinations,
	manifest *backup.ClusterManifest,
	replicasetUUID string,
) (DownloadItem, string, error) {
	instance := manifest.Shards[replicasetUUID].Instance
	if instance == nil {
		return DownloadItem{}, "", fmt.Errorf(
			"backup %s carries no archive for replicaset %s",
			manifest.BackupID, replicasetUUID)
	}

	key, err := storage.CleanKey(instance.Artifact.Path)
	if err != nil {
		return DownloadItem{}, "", fmt.Errorf(
			"backup %s: artifact path %q of replicaset %s is not a storage key: %w",
			manifest.BackupID, instance.Artifact.Path, replicasetUUID, err)
	}

	destination, err := claimed.claim(dir, key)
	if err != nil {
		return DownloadItem{}, "", fmt.Errorf("backup %s: %w", manifest.BackupID, err)
	}

	expected := instance.Artifact.ChecksumSHA256

	checksum, err := fetch(ctx, store, key, destination, expected)
	if err != nil {
		return DownloadItem{}, "", fmt.Errorf(
			"failed to download the archive of backup %s: %w", manifest.BackupID, err)
	}

	warning := ""
	if expected == "" {
		warning = fmt.Sprintf(
			"backup %s: the manifest records no checksum for replicaset %s, so %s "+
				"was taken on trust; its sum is %s",
			manifest.BackupID, replicasetUUID, key, checksum)
	}

	return DownloadItem{
		BackupID:       string(manifest.BackupID),
		Type:           instance.Artifact.Type,
		Artifact:       destination,
		ChecksumSHA256: checksum,
	}, warning, nil
}

// planBackupIDs returns every backup the plan touches, each once and in a
// stable order: one manifest covers the whole cluster, so the same backup shows
// up in as many shard chains as it has replicasets.
func planBackupIDs(plan chain.Plan) []backup.BackupID {
	seen := make(map[backup.BackupID]bool)
	for _, shard := range plan.Shards {
		for _, manifest := range shard.Backups {
			seen[manifest.BackupID] = true
		}
	}

	return slices.Sorted(maps.Keys(seen))
}
