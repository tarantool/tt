package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/storage"
)

// Load reads every stored manifest and builds a fully marked chain.
func Load(ctx context.Context, store storage.Storage) (*Chain, error) {
	manifests, err := loadManifests(ctx, store)
	if errors.Is(err, errManifestVanished) {
		manifests, err = loadManifests(ctx, store)
		if errors.Is(err, errManifestVanished) {
			return nil, fmt.Errorf("load manifests: %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("load manifests: %w", err)
	}

	chain, err := Build(manifests)
	if err != nil {
		return nil, fmt.Errorf("build chain: %w", err)
	}
	return chain, nil
}

// errManifestVanished marks a manifest that LIST reported but GET could not find.
var errManifestVanished = errors.New("manifest vanished between list and get")

// loadManifests lists and reads every manifest once, reporting errManifestVanished
// when a listed key is missing on GET so Load can retry.
func loadManifests(
	ctx context.Context,
	store storage.Storage,
) ([]*backup.ClusterManifest, error) {
	objects, err := store.List(ctx, storage.ManifestsPrefix())
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}

	manifests := make([]*backup.ClusterManifest, 0, len(objects))
	for _, object := range objects {
		data, err := storage.GetBytes(ctx, store, object.Key)
		if errors.Is(err, storage.ErrKeyNotFound) {
			return nil, fmt.Errorf("%w: %q", errManifestVanished, object.Key)
		}
		if err != nil {
			return nil, fmt.Errorf("load manifest %q: %w", object.Key, err)
		}

		var manifest backup.ClusterManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("decode manifest %q: %w", object.Key, err)
		}

		manifests = append(manifests, &manifest)
	}

	return manifests, nil
}

// Build groups manifests, attaches own and inherited chain problems, and
// stitches cluster recovery points.
func Build(manifests []*backup.ClusterManifest) (*Chain, error) {
	// Index every backup_id first, then reverse the previous_backup_id links.
	entries := make(map[backup.BackupID]*Entry, len(manifests))
	groups := make(map[backup.BackupID]*Group)
	for i, manifest := range manifests {
		if manifest == nil {
			return nil, fmt.Errorf("manifest %d is nil", i)
		}
		if _, exists := entries[manifest.BackupID]; exists {
			return nil, fmt.Errorf("duplicate backup_id %q", manifest.BackupID)
		}

		entry := &Entry{Manifest: manifest}
		entries[manifest.BackupID] = entry
		baseID := manifest.BaseFullBackupID
		if groups[baseID] == nil {
			groups[baseID] = &Group{}
		}
		groups[baseID].Entries = append(groups[baseID].Entries, entry)
	}

	// Reverse previous_backup_id links after every backup_id is indexed.
	children := make(map[backup.BackupID][]*Entry, len(entries))
	for _, entry := range entries {
		if previousID := entry.Manifest.PreviousBackupID; previousID != "" {
			children[previousID] = append(children[previousID], entry)
		}
	}
	for previousID := range children {
		slices.SortFunc(children[previousID], compareEntries)
	}

	markProblems(entries, children)

	orderedGroups := orderGroups(groups, children)
	byID := make(map[backup.BackupID]*Entry, len(entries))
	for i := range orderedGroups {
		for _, entry := range orderedGroups[i].Entries {
			byID[entry.Manifest.BackupID] = entry
		}
	}
	segments := buildSegments(orderedGroups)
	return &Chain{
		groups:   orderedGroups,
		byID:     byID,
		points:   buildClusterPoints(segments),
		segments: segments,
	}, nil
}

// markProblems finds broken links and immediately propagates each problem to
// every dependent manifest.
func markProblems(
	entries map[backup.BackupID]*Entry,
	children map[backup.BackupID][]*Entry,
) {
	// A structurally invalid manifest poisons its whole tail.
	for _, entry := range entries {
		if err := entry.Manifest.Validate(); err != nil {
			propagateProblem(entry, &Problem{
				Kind:     ProblemInvalidManifest,
				BackupID: string(entry.Manifest.BackupID),
				Detail:   fmt.Sprintf("manifest validation failed: %v", err),
			}, children)
		}
	}

	// Orphan and vclock mismatch belong to one entry and poison its whole tail.
	for _, entry := range entries {
		previousID := entry.Manifest.PreviousBackupID
		parent := entries[previousID]
		if previousID != "" && parent == nil {
			propagateProblem(entry, &Problem{
				Kind:     ProblemOrphan,
				BackupID: string(entry.Manifest.BackupID),
				Detail:   fmt.Sprintf("previous backup %q not found", previousID),
			}, children)
		}
		if parent == nil {
			continue
		}
		for _, problem := range vclockProblems(entry, entries) {
			propagateProblem(entry, problem, children)
		}
	}

	// A fork poisons every branch, not the common parent before the fork.
	for previousID, forks := range children {
		if len(forks) < 2 {
			continue
		}
		ids := make([]string, 0, len(forks))
		for _, entry := range forks {
			ids = append(ids, string(entry.Manifest.BackupID))
		}
		detail := fmt.Sprintf(
			"previous backup %q has multiple children: %s",
			previousID,
			strings.Join(ids, ", "),
		)
		for _, entry := range forks {
			propagateProblem(entry, &Problem{
				Kind:     ProblemFork,
				BackupID: string(entry.Manifest.BackupID),
				Detail:   detail,
			}, children)
		}
	}
}

// propagateProblem attaches a problem to its source and creates an inherited
// copy for each descendant. All writes affect the Entries stored in groups.
func propagateProblem(
	source *Entry,
	problem *Problem,
	children map[backup.BackupID][]*Entry,
) {
	source.Problems = append(source.Problems, problem)

	// Guard against cycles in previous_backup_id to keep Build finite.
	visited := map[backup.BackupID]bool{source.Manifest.BackupID: true}
	pending := append([]*Entry(nil), children[source.Manifest.BackupID]...)
	for len(pending) > 0 {
		child := pending[0]
		pending = pending[1:]
		childID := child.Manifest.BackupID
		if visited[childID] {
			continue
		}
		visited[childID] = true
		child.Problems = append(child.Problems, &Problem{
			Kind:      problem.Kind,
			BackupID:  string(childID),
			Inherited: true,
			Detail:    problem.Detail,
		})
		pending = append(pending, children[childID]...)
	}
}

// vclockProblems flags an incremental shard whose vclock_begin does not continue
// the vclock_end of the nearest ancestor carrying the same shard.
func vclockProblems(entry *Entry, entries map[backup.BackupID]*Entry) []*Problem {
	replicasets := make([]string, 0, len(entry.Manifest.Shards))
	for replicasetUUID := range entry.Manifest.Shards {
		replicasets = append(replicasets, replicasetUUID)
	}
	slices.Sort(replicasets)

	var problems []*Problem
	for _, replicasetUUID := range replicasets {
		current := entry.Manifest.Shards[replicasetUUID].Instance
		if current == nil || current.Artifact.Type != backup.BackupTypeIncremental {
			continue
		}

		// Find the nearest ancestor manifest that carries this shard.
		ancestor, previous := nearestShardAncestor(entry, replicasetUUID, entries)
		if previous == nil {
			// No ancestor carries this shard: nothing to stitch against.
			continue
		}
		if maps.Equal(current.VclockBegin, previous.VclockEnd) {
			continue
		}
		problems = append(problems, &Problem{
			Kind:     ProblemVclockMismatch,
			BackupID: string(entry.Manifest.BackupID),
			Detail: fmt.Sprintf(
				"replicaset %q vclock_begin %v does not match backup %q vclock_end %v",
				replicasetUUID,
				current.VclockBegin,
				ancestor.Manifest.BackupID,
				previous.VclockEnd,
			),
		})
	}
	return problems
}

// nearestShardAncestor walks previous_backup_id backwards and returns the first
// ancestor carrying the given shard as an instance, skipping degraded ancestors.
// Returns nil when none exists.
func nearestShardAncestor(
	entry *Entry,
	replicasetUUID string,
	entries map[backup.BackupID]*Entry,
) (*Entry, *backup.ShardInstance) {
	visited := map[backup.BackupID]bool{entry.Manifest.BackupID: true}
	current := entry
	for {
		previousID := current.Manifest.PreviousBackupID
		if previousID == "" {
			return nil, nil
		}
		ancestor := entries[previousID]
		if ancestor == nil || visited[previousID] {
			return nil, nil
		}
		visited[previousID] = true
		if instance := ancestor.Manifest.Shards[replicasetUUID].Instance; instance != nil {
			return ancestor, instance
		}
		current = ancestor
	}
}

// orderGroups puts each full first, follows its children, and then orders the
// groups from oldest full backup to newest.
func orderGroups(
	byBase map[backup.BackupID]*Group,
	children map[backup.BackupID][]*Entry,
) []Group {
	groups := make([]Group, 0, len(byBase))
	for baseID, group := range byBase {
		orderGroup(group, baseID, children)
		groups = append(groups, *group)
	}
	slices.SortFunc(groups, func(left, right Group) int {
		if len(left.Entries) == 0 || len(right.Entries) == 0 {
			return len(left.Entries) - len(right.Entries)
		}
		return compareEntries(left.Entries[0], right.Entries[0])
	})
	return groups
}

// orderGroup walks the declared full first and follows previous_backup_id
// links. Disconnected tails are appended afterwards for verify diagnostics.
func orderGroup(
	group *Group,
	baseID backup.BackupID,
	children map[backup.BackupID][]*Entry,
) {
	slices.SortFunc(group.Entries, compareEntries)
	ordered := make([]*Entry, 0, len(group.Entries))
	visited := make(map[backup.BackupID]bool, len(group.Entries))
	members := make(map[backup.BackupID]bool, len(group.Entries))
	for _, entry := range group.Entries {
		members[entry.Manifest.BackupID] = true
	}

	var appendTree func(*Entry)
	appendTree = func(entry *Entry) {
		id := entry.Manifest.BackupID
		if visited[id] || entry.Manifest.BaseFullBackupID != baseID {
			return
		}
		visited[id] = true
		ordered = append(ordered, entry)
		for _, child := range children[id] {
			appendTree(child)
		}
	}

	for _, entry := range group.Entries {
		if entry.Manifest.BackupID == baseID {
			appendTree(entry)
			break
		}
	}
	// Append disconnected tails root-first (an entry whose previous is not a group
	// member is a tail root), keeping parents before children in Problems().
	for _, entry := range group.Entries {
		if members[entry.Manifest.PreviousBackupID] {
			continue
		}
		appendTree(entry)
	}
	for _, entry := range group.Entries {
		appendTree(entry)
	}
	group.Entries = ordered
}
