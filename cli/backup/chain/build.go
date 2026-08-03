package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/storage"
)

// Unreadable is a listed manifest that could not be turned into a chain entry.
type Unreadable struct {
	// Key is the storage key of the manifest.
	Key string
	// Err explains why the manifest could not be used.
	Err error
}

// Load reads every stored manifest and builds a fully marked chain. A manifest
// that cannot be read or decoded fails the whole load: a chain silently missing
// a link would send its caller down a broken recovery path. Callers that
// diagnose the storage rather than recover from it use LoadPartial.
func Load(ctx context.Context, store storage.Storage) (*Chain, error) {
	chain, unreadable, err := LoadPartial(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("load chain: %w", err)
	}

	if len(unreadable) > 0 {
		return nil, fmt.Errorf(
			"load manifest %q: %w", unreadable[0].Key, unreadable[0].Err,
		)
	}

	return chain, nil
}

// LoadPartial builds the chain from the manifests it could read and returns the
// ones it could not, so that a diagnostic caller (tt backup verify) reports a
// single broken object instead of going blind on the whole storage.
func LoadPartial(
	ctx context.Context,
	store storage.Storage,
) (*Chain, []Unreadable, error) {
	loaded, unreadable, err := loadManifests(ctx, store)
	if err != nil {
		return nil, nil, fmt.Errorf("load manifests: %w", err)
	}

	// A manifest that LIST reported but GET could not find is usually gc
	// deleting it mid-run: redo the listing once before believing it.
	if slices.ContainsFunc(unreadable, isVanished) {
		loaded, unreadable, err = loadManifests(ctx, store)
		if err != nil {
			return nil, nil, fmt.Errorf("load manifests: %w", err)
		}
	}

	// A manifest with no backup_id, or two claiming the same one, would make
	// Build reject the storage whole, leaving the diagnostic caller with nothing
	// to report about the very defects it exists to name.
	manifests, unusable := splitUnusable(loaded)
	unreadable = append(unreadable, unusable...)

	chain, err := Build(manifests)
	if err != nil {
		return nil, nil, fmt.Errorf("build chain: %w", err)
	}

	return chain, unreadable, nil
}

// errManifestVanished marks a manifest that LIST reported but GET could not find.
var errManifestVanished = errors.New("manifest vanished between list and get")

// isVanished reports whether a manifest was listed but missing on GET.
func isVanished(unreadable Unreadable) bool {
	return errors.Is(unreadable.Err, errManifestVanished)
}

// loadedManifest pairs a decoded manifest with the key it came from. A defect
// that only the key can name - two objects claiming one backup_id - is otherwise
// impossible to report against the right object.
type loadedManifest struct {
	Key      string
	Manifest *backup.ClusterManifest
}

// errDuplicateBackupID marks manifests sharing one backup_id.
var errDuplicateBackupID = errors.New("duplicate backup_id")

// errMissingBackupID marks a manifest with nothing to key it by.
var errMissingBackupID = errors.New("manifest has no backup_id")

// splitUnusable separates out manifests that cannot become chain entries: one
// carrying no backup_id has nothing to key it by, and of two objects claiming
// the same id neither can be told to be the authentic one. Both are reported
// against their storage key - the only name they have left - while the rest of
// the storage is still checked.
func splitUnusable(loaded []loadedManifest) ([]*backup.ClusterManifest, []Unreadable) {
	claims := make(map[backup.BackupID]int, len(loaded))
	for _, item := range loaded {
		claims[item.Manifest.BackupID]++
	}

	manifests := make([]*backup.ClusterManifest, 0, len(loaded))
	unusable := make([]Unreadable, 0)

	for _, item := range loaded {
		switch id := item.Manifest.BackupID; {
		case id == "":
			unusable = append(unusable, Unreadable{Key: item.Key, Err: errMissingBackupID})
		case claims[id] > 1:
			unusable = append(unusable, Unreadable{
				Key: item.Key,
				Err: fmt.Errorf("%w %q: another object claims it too",
					errDuplicateBackupID, id),
			})
		default:
			manifests = append(manifests, item.Manifest)
		}
	}

	slices.SortFunc(unusable, func(a, b Unreadable) int {
		return strings.Compare(a.Key, b.Key)
	})

	return manifests, unusable
}

// loadManifests lists and reads every manifest once, collecting the ones that
// could not be read or decoded instead of failing on the first of them. Only a
// failure to list at all is returned as an error.
func loadManifests(
	ctx context.Context,
	store storage.Storage,
) ([]loadedManifest, []Unreadable, error) {
	objects, err := store.List(ctx, storage.ManifestsPrefix())
	if err != nil {
		return nil, nil, fmt.Errorf("list manifests: %w", err)
	}

	manifests := make([]loadedManifest, 0, len(objects))
	unreadable := make([]Unreadable, 0)

	for _, object := range objects {
		manifest, err := loadManifest(ctx, store, object.Key)
		if err != nil {
			unreadable = append(unreadable, Unreadable{Key: object.Key, Err: err})
			continue
		}

		manifests = append(manifests, loadedManifest{Key: object.Key, Manifest: manifest})
	}

	return manifests, unreadable, nil
}

// loadManifest reads and decodes one manifest.
func loadManifest(
	ctx context.Context,
	store storage.Storage,
	key string,
) (*backup.ClusterManifest, error) {
	data, err := storage.GetBytes(ctx, store, key)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return nil, fmt.Errorf("%w: %q", errManifestVanished, key)
		}

		return nil, fmt.Errorf("read manifest %q: %w", key, err)
	}

	var manifest backup.ClusterManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest %q: %w", key, err)
	}

	return &manifest, nil
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

	for _, entry := range sortedEntries(entries) {
		if previousID := backup.BackupID(entry.Manifest.PreviousBackupID); previousID != "" {
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
		groups:        orderedGroups,
		byID:          byID,
		points:        buildClusterPoints(segments),
		segments:      segments,
		coverageStart: coverageStart(manifests),
	}, nil
}

// coverageStart returns the creation time of the earliest manifest: the moment
// from which the storage holds anything at all. Problematic manifests count -
// a time before them is out of range whether or not they can be recovered from.
func coverageStart(manifests []*backup.ClusterManifest) time.Time {
	var earliest time.Time
	for _, manifest := range manifests {
		created := manifest.CreationTime
		if created.IsZero() {
			continue
		}

		if earliest.IsZero() || created.Before(earliest) {
			earliest = created
		}
	}

	return earliest
}

// markProblems finds broken links and immediately propagates each problem to
// every dependent manifest.
func markProblems(
	entries map[backup.BackupID]*Entry,
	children map[backup.BackupID][]*Entry,
) {
	// A structurally invalid manifest poisons its whole tail.
	for _, entry := range sortedEntries(entries) {
		if err := entry.Manifest.Validate(); err != nil {
			propagateProblem(entry, &Problem{
				Kind:     ProblemInvalidManifest,
				BackupID: string(entry.Manifest.BackupID),
				Detail:   fmt.Sprintf("manifest validation failed: %v", err),
			}, children)
		}
	}

	// Orphan and vclock mismatch belong to one entry and poison its whole tail.
	for _, entry := range sortedEntries(entries) {
		previousID := backup.BackupID(entry.Manifest.PreviousBackupID)
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

	// A manifest whose previous_backup_id leads back to itself has no full backup
	// to replay from. Left unflagged it would also drop out of the ordered group
	// (it is reachable neither from the base nor as a tail root), and a manifest
	// no command can see is one gc would happily delete around.
	for _, entry := range sortedEntries(entries) {
		if cycle := cycleThrough(entry, entries); cycle != "" {
			propagateProblem(entry, &Problem{
				Kind:     ProblemInvalidManifest,
				BackupID: string(entry.Manifest.BackupID),
				Detail:   fmt.Sprintf("previous_backup_id forms a cycle through %q", cycle),
			}, children)
		}
	}

	// A fork poisons every branch, not the common parent before the fork.
	for _, previousID := range slices.Sorted(maps.Keys(children)) {
		forks := children[previousID]
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

// sortedEntries returns the entries ordered by backup id. Every loop that
// attaches problems must be deterministic: map iteration order would otherwise
// decide in which order an entry collects problems inherited from several
// ancestors, and that order reaches the verify report, where a cron job diffing
// --format json would see changes that are not there.
func sortedEntries(entries map[backup.BackupID]*Entry) []*Entry {
	ordered := make([]*Entry, 0, len(entries))
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		ordered = append(ordered, entries[id])
	}

	return ordered
}

// cycleThrough walks previous_backup_id back from an entry and returns the
// backup id where the walk meets itself, or an empty id when the walk ends.
func cycleThrough(entry *Entry, entries map[backup.BackupID]*Entry) backup.BackupID {
	visited := map[backup.BackupID]bool{entry.Manifest.BackupID: true}

	for current := entry; ; {
		previousID := backup.BackupID(current.Manifest.PreviousBackupID)
		if previousID == "" {
			return ""
		}

		parent, ok := entries[previousID]
		if !ok {
			return ""
		}

		if visited[previousID] {
			return previousID
		}

		visited[previousID] = true
		current = parent
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
		previousID := backup.BackupID(current.Manifest.PreviousBackupID)
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
		if members[backup.BackupID(entry.Manifest.PreviousBackupID)] {
			continue
		}

		appendTree(entry)
	}

	// Whatever is still unvisited is reachable neither from the full backup nor
	// from a tail root, which means its links close a cycle. Such a manifest is
	// marked as a problem, but it must not silently disappear from the chain:
	// every consumer decides what to do with a manifest it can see, and none can
	// account for one it cannot.
	for _, entry := range group.Entries {
		if !visited[entry.Manifest.BackupID] {
			visited[entry.Manifest.BackupID] = true
			ordered = append(ordered, entry)
		}
	}

	group.Entries = ordered
}
