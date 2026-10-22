package restore

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk/backup/storage"
	"github.com/tarantool/tt/v3/cli/backup"
	"github.com/tarantool/tt/v3/cli/backup/chain"
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
	// Replicasets restricts the restore to the named replicasets, the rest
	// being left to be bootstrapped fresh. The names are the configuration's,
	// so it requires Current; empty restores every replicaset the backup holds.
	Replicasets []string
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

// RestoreTarget names the instances a replicaset's chain is restored onto, and
// the instance UUID each of them has to own once it is.
//
// Every member the backup recorded is restored in place: each node replays
// the replicaset's chain and comes up as itself, so a member with a uuid of
// its own in the manifest keeps it. The members the configuration carries
// but the backup never saw are the ones wiped and bootstrapped fresh:
// Tarantool 3.x identifies an instance by name, so such a member boots
// empty, reclaims its _cluster record under a fresh UUID and needs none of
// its own.
type RestoreTarget struct {
	// InstanceName is the node this replicaset's chain goes to: the master,
	// the instance the backup was taken on. It is the master's own entry of
	// Instances, kept at the top level for a reader of the older document.
	InstanceName string `json:"instance_name"`
	// PatchUUID is the instance UUID the restored master must carry: the one
	// it held in the backed-up cluster, which is what `tt restore apply
	// --patch-uuid` stamps into every header. Restoring onto the instance the
	// backup was taken on makes it the UUID already in the files, and the patch
	// a confirmation. It is absent only when the manifest recorded no UUID at
	// all, and then a warning says so rather than an empty value going out as
	// if it meant "do not patch".
	PatchUUID string `json:"patch_uuid,omitempty"`
	// Instances names every node the chain is replayed onto, the master first
	// and the rest by name, each with the instance UUID that node owned in
	// the backed-up cluster - `tt restore apply` runs per instance with its
	// own --patch-uuid. A manifest that recorded no members beside the master
	// lists it alone, and a warning says so: single-target restore.
	Instances []RestoreInstance `json:"instances"`
	// Rejoin are the configured instances of the replicaset the backup holds
	// no member for: wiped before the restore, joining the replicaset from
	// nothing once it is up. Filled only when a cluster config was given,
	// because the backup does not know the composition of the cluster being
	// restored into.
	Rejoin []string `json:"rejoin,omitempty"`
}

// RestoreInstance is one node a replicaset's chain is replayed onto.
type RestoreInstance struct {
	// InstanceName is the node this chain goes to.
	InstanceName string `json:"instance_name"`
	// PatchUUID is the instance UUID the restored node must carry: the one it
	// held in the backed-up cluster, read off the manifest's topology entry
	// for it. Absent when the manifest recorded none for the member.
	PatchUUID string `json:"patch_uuid,omitempty"`
	// Role is the member's place in the replicaset: master for the instance
	// the archives were taken on, replica for every other member.
	Role string `json:"role"`
}

// The role of a restore instance: the master is the instance the archives
// were taken on, every other member of the replicaset a replica.
const (
	roleMaster  = "master"
	roleReplica = "replica"
)

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
	selected *selection,
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
			opts.TargetTime, selected)
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

	selected, err := resolveSelection(opts.Current, opts.Replicasets)
	if err != nil {
		return nil, err
	}

	backupChain, err := chain.Load(ctx, opts.Storage,
		chain.WithSelection(selected.chainSelection(opts.Current)))
	if err != nil {
		return nil, fmt.Errorf("failed to build the backup chain: %w", err)
	}

	result := new(PlanResult)

	result.TargetTime = opts.TargetTime.UTC()
	result.Warnings = make([]string, 0)

	// A selected replicaset the storage holds nowhere fails every point alike,
	// and it fails them before there is a point: nothing is stitched over it.
	// Named here, it is the same refusal the comparison would have given.
	if selected != nil {
		absent := selected.absent(*opts.Current, backupChain.Manifests())
		if len(absent) > 0 {
			result.Status = StatusTopologyMismatch
			result.Reason = reasonFor(StatusTopologyMismatch)
			result.TopologyDiff = &TopologyDiff{
				ExtraReplicasets: absent, MissingReplicasets: nil,
				AmbiguousReplicasets: nil, UncoveredMasters: nil,
			}
			result.Warnings = append(result.Warnings,
				selected.unselectedWarnings(*opts.Current)...)

			return result, nil
		}
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

	topology := checkTopology(point, recoveryPlan, opts.Current, selected)

	result.Warnings = append(result.Warnings, topology.warnings...)

	diff := topology.diff

	if diff != nil {
		refuseTopology(result, diff, backupChain, opts, selected)

		return result, nil
	}

	downloads, downloadWarnings, err := download(ctx, opts.Storage, dir, recoveryPlan)
	if err != nil {
		return nil, err
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
//
//nolint:err113 // Preserve established restore error messages.
func ParseTargetTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return t.UTC(), nil
	}

	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err == nil {
		return time.Unix(seconds, 0).UTC(), nil
	}

	return time.Time{}, fmt.Errorf(
		"--target-time %q is neither an RFC 3339 timestamp (2026-03-25T10:30:00Z) "+
			"nor a unix timestamp", raw,
	)
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
//
// Both sides are narrowed to the selection, so that the comparison weighs the
// replicasets being restored against the ones asked for. What the selection
// leaves out is reported afterwards, once per configured replicaset.
func checkTopology(
	point chain.ClusterPoint,
	plan chain.Plan,
	current *ClusterTopology,
	selected *selection,
) planTopology {
	shards, warnings := planShards(plan)

	if current == nil {
		targets, targetWarnings := restoreTargets(shards, nil)

		return planTopology{
			diff:    nil,
			targets: targets,
			warnings: append(append([]string{noTopologyCheckWarning}, warnings...),
				targetWarnings...),
		}
	}

	match := matchTopology(pointTopology(point), selected.configured(*current))

	match.diff.UncoveredMasters = uncoveredMasters(masterNames(shards), match.matched)

	targets, targetWarnings := restoreTargets(shards, match.matched)

	result := planTopology{
		diff:    nil,
		targets: targets,
		warnings: append(append(append(match.warnings, warnings...),
			targetWarnings...),
			selected.unselectedWarnings(*current)...),
	}

	if !match.diff.Empty() {
		result.diff = &match.diff
	}

	return result
}

// planShards returns, per replicaset, what the last backup of its chain says
// about it. It is the last backup that decides, because that is the manifest
// the point itself was found in: a chain can span a master change, and then the
// earlier backups of it name the instance that used to hold the role rather
// than the one whose archive the restore lands on.
func planShards(plan chain.Plan) (map[string]backedUpShard, []string) {
	shards := make(map[string]backedUpShard, len(plan.Shards))
	unknown := make([]string, 0)

	for _, replicasetUUID := range slices.Sorted(maps.Keys(plan.Shards)) {
		backups := plan.Shards[replicasetUUID].Backups
		if len(backups) == 0 {
			continue
		}

		manifest := backups[len(backups)-1]
		instance := manifest.Shards[replicasetUUID].Instance

		if instance == nil || instance.InstanceName == "" {
			unknown = append(unknown, fmt.Sprintf(
				"replicaset %s: the backup does not name the instance it was taken "+
					"on, so the config cannot be checked for it", replicasetUUID,
			))

			continue
		}

		shards[replicasetUUID] = backedUpShard{
			backupID: manifest.BackupID,
			instance: instance,
			members:  manifest.Topology.Replicasets[replicasetUUID],
		}
	}

	return shards, unknown
}

// backedUpShard is what the manifest the point was resolved in says about one
// replicaset: the instance its archive was taken on, and the membership that
// manifest recorded beside it.
type backedUpShard struct {
	// backupID is the manifest the membership and the master are read from:
	// the last backup of the replicaset's chain.
	backupID backup.BackupID
	// instance is the master the archive of the last backup was taken on.
	instance *backup.ShardInstance
	// members are the topology entries of that same manifest: the replicaset's
	// membership as the backup recorded it, master included. A manifest
	// written before members were recorded lists the master alone.
	members []backup.TopologyInstance
}

// restoreTargets names, per replicaset, the nodes its chain is replayed onto
// and the UUID each carries afterwards, plus the configured members that are
// wiped and join the replicaset from nothing instead. The second result
// carries the warnings of building the targets themselves: a manifest that
// recorded no uuid for its own master, and one that recorded no members
// beside him.
func restoreTargets(
	shards map[string]backedUpShard,
	matched map[string]ConfiguredReplicaset,
) (map[string]RestoreTarget, []string) {
	targets := make(map[string]RestoreTarget, len(shards))
	warnings := make([]string, 0)

	for _, replicasetUUID := range slices.Sorted(maps.Keys(shards)) {
		shard := shards[replicasetUUID]
		master := shard.instance

		target := RestoreTarget{
			Rejoin:       nil,
			InstanceName: master.InstanceName,
			PatchUUID:    master.InstanceUUID,
			// The master is always a target of his own: the archive is his, so
			// his headers are the ones the chain already fits.
			Instances: []RestoreInstance{{
				InstanceName: master.InstanceName,
				PatchUUID:    master.InstanceUUID,
				Role:         roleMaster,
			}},
		}

		if master.InstanceUUID == "" {
			warnings = append(warnings, fmt.Sprintf(
				"replicaset %s: the backup records no instance uuid for %s, so "+
					"the plan carries no patch_uuid; restore onto that same "+
					"instance and omit --patch-uuid, whose headers already "+
					"carry it",
				replicasetUUID, master.InstanceName,
			))
		}

		// The membership the rejoin list is drawn against: the names the
		// manifest recorded, the master always among them - a manifest with no
		// topology entry for its own master still backed him up.
		membership := []string{master.InstanceName}

		for _, member := range memberEntries(shard.members, master.InstanceName) {
			membership = append(membership, member.InstanceName)

			// A member needs a node to replay onto. With a cluster
			// configuration that node is named by it, and a member the
			// configuration no longer carries is reported by the comparison
			// and left out; without one the manifest's own record is all the
			// plan has, and every member it names is a target as it stands.
			replicaset, configured := matched[replicasetUUID]
			if !configured || slices.Contains(replicaset.Instances, member.InstanceName) {
				target.Instances = append(target.Instances, RestoreInstance{
					InstanceName: member.InstanceName,
					PatchUUID:    member.InstanceUUID,
					Role:         roleReplica,
				})
			}
		}

		nameless := namelessMembers(shard.members)

		if len(nameless) > 0 {
			uuids := make([]string, 0, len(nameless))
			for _, member := range nameless {
				uuids = append(uuids, member.InstanceUUID)
			}

			warnings = append(warnings, fmt.Sprintf(
				"backup %s: replicaset %s carries %s without a name "+
					"(uuid %s): it is neither a restore target nor named in "+
					"rejoin -- name it and back up again, or restore it by hand",
				shard.backupID, replicasetUUID,
				pluralize(len(nameless), "a member", "%d members"),
				strings.Join(uuids, ", "),
			))
		}

		if len(shard.members) <= 1 {
			warnings = append(warnings, fmt.Sprintf(
				"backup %s records no members for replicaset %s: "+
					"single-target restore", shard.backupID, replicasetUUID,
			))
		}

		replicaset, ok := matched[replicasetUUID]

		if ok {
			target.Rejoin = missing(replicaset.Instances, membership)
		}

		targets[replicasetUUID] = target
	}

	return targets, warnings
}

// pluralize names a count of things for a warning: the singular word for
// one, the plural format filled with the count for more.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return fmt.Sprintf(many, n)
}

// namelessMembers returns the backed-up members whose _cluster rows carry
// no name. A restore matches members by name, so one of these can be
// neither a target nor a rejoin, and the plan says so rather than dropping
// the member silently.
func namelessMembers(members []backup.TopologyInstance) []backup.TopologyInstance {
	var nameless []backup.TopologyInstance

	for _, member := range members {
		if member.InstanceName == "" {
			nameless = append(nameless, member)
		}
	}

	return nameless
}

// memberEntries returns the members of a backed-up replicaset beside its
// master: named entries only, ordered by name, the master's own entry left
// out. A member without a name cannot be matched against a configuration,
// and the plan has nothing to say about it.
func memberEntries(
	members []backup.TopologyInstance,
	master string,
) []backup.TopologyInstance {
	named := make([]backup.TopologyInstance, 0, len(members))
	for _, member := range members {
		if member.InstanceName != "" && member.InstanceName != master {
			named = append(named, member)
		}
	}

	slices.SortFunc(named, func(a, b backup.TopologyInstance) int {
		return strings.Compare(a.InstanceName, b.InstanceName)
	})

	return named
}

// masterNames reduces the backed-up shards to the master names the topology
// comparison works on.
func masterNames(shards map[string]backedUpShard) map[string]string {
	names := make(map[string]string, len(shards))
	for replicasetUUID, shard := range shards {
		names[replicasetUUID] = shard.instance.InstanceName
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
	selected *selection,
) *NearestSafe {
	var before, after *time.Time

	configured := selected.configured(current)

	for _, point := range backupChain.ClusterPoints() {
		if matchTopology(pointTopology(point), configured).diff.replicasetLevel() {
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
	err := os.MkdirAll(dir, downloadDirPerm)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create %q: %w", dir, err)
	}

	claimed := make(destinations)

	// Manifests first: they are small, and a chain whose metadata cannot be
	// fetched is not worth spending an archive download on.
	for _, backupID := range planBackupIDs(plan) {
		key := storage.ManifestKey(string(backupID))

		destination, err := claimed.claim(dir, key)
		if err != nil {
			return nil, nil, err
		}

		_, err = fetch(ctx, store, key, destination, "")
		if err != nil {
			return nil, nil, fmt.Errorf(
				"failed to download the manifest of backup %s: %w", backupID, err,
			)
		}
	}

	downloads := make(map[string][]DownloadItem, len(plan.Shards))
	warnings := make([]string, 0)

	for _, replicasetUUID := range slices.Sorted(maps.Keys(plan.Shards)) {
		shard := plan.Shards[replicasetUUID]
		items := make([]DownloadItem, 0, len(shard.Backups))

		for _, manifest := range shard.Backups {
			item, unverified, err := downloadArchive(
				ctx, store, dir, claimed, manifest, replicasetUUID,
			)
			if err != nil {
				return nil, nil, err
			}

			if unverified != "" {
				warnings = append(warnings, unverified)
			}

			items = append(items, item)
		}

		// The recovery point sits inside the last archive of the chain; a nil
		// TrimTo means it sits exactly at its end, and then there is nothing to
		// cut off.
		last := len(items) - 1

		if last >= 0 && shard.TrimTo != nil {
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
//
//nolint:err113 // Preserve established restore error messages.
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
			manifest.BackupID, replicasetUUID,
		)
	}

	key, err := storage.CleanKey(instance.Artifact.Path)
	if err != nil {
		return DownloadItem{}, "", fmt.Errorf(
			"backup %s: artifact path %q of replicaset %s is not a storage key: %w",
			manifest.BackupID, instance.Artifact.Path, replicasetUUID, err,
		)
	}

	destination, err := claimed.claim(dir, key)
	if err != nil {
		return DownloadItem{}, "", fmt.Errorf("backup %s: %w", manifest.BackupID, err)
	}

	expected := instance.Artifact.ChecksumSHA256

	checksum, err := fetch(ctx, store, key, destination, expected)
	if err != nil {
		return DownloadItem{}, "", fmt.Errorf(
			"failed to download the archive of backup %s: %w", manifest.BackupID, err,
		)
	}

	warning := ""
	if expected == "" {
		warning = fmt.Sprintf(
			"backup %s: the manifest records no checksum for replicaset %s, so %s "+
				"was taken on trust; its sum is %s",
			manifest.BackupID, replicasetUUID, key, checksum,
		)
	}

	return DownloadItem{
		TrimXlog: false, TrimTo: nil,
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
