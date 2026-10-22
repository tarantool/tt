// Package restore prepares a node's work directory from backup archives so
// Tarantool can start on a chosen recovery point.
package restore

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"

	"github.com/tarantool/tt/sdk/backup/archive"
	"github.com/tarantool/tt/v3/cli/backup"
	"github.com/tarantool/tt/v3/cli/backup/xlog"
)

// fragmentEntryName is the per-shard manifest fragment inside an archive. It
// describes the backup, not the instance state, so it never lands in the work
// directory -- but it is where the archive says which part of the journal it
// covers, which is what puts a chain in order.
const fragmentEntryName = "instance_backup.json"

// fragmentSizeLimit caps the manifest fragment read out of an archive. It is a
// few hundred bytes of JSON, and it is read before anything about the archive
// has been established.
const fragmentSizeLimit = 1 << 20

// patchTmpSuffix names the copy used when a UUID cannot be swapped in place.
const patchTmpSuffix = ".uuidpatch"

// trimTmpSuffix names the truncated xlog before it replaces the original.
const trimTmpSuffix = ".trimmed"

const restoredFileMode = 0o644
const restoredDirMode = 0o755
const indexExt = ".index"
const runExt = ".run"
const xlogExt = ".xlog"
const vylogExt = ".vylog"

// restoreArtifactExts are the files a restore owns in the work directory:
// everything Tarantool replays, plus the half-finished files this command and
// go-xlog leave behind when they are interrupted. Cleanup removes exactly
// these, so a work directory holding an instance's config or logs keeps them.
var restoreArtifactExts = []string{
	snapshotExt, xlogExt, vylogExt, runExt, indexExt, sortDataExt,
	inProgressExt, ".uuidbak", patchTmpSuffix, trimTmpSuffix,
}

// patchedExts are the files whose header carries an instance UUID. Every one
// of them is checked at startup, not just the journals: a .vylog left on the
// backed-up master's UUID fails recovery outright with "invalid instance
// UUID", and the vinyl run/index files carry the same field.
var patchedExts = []string{snapshotExt, xlogExt, vylogExt, runExt, indexExt}

// journalExts are the files Tarantool names after the vclock signature they
// start at. Vinyl's .run and .index are numbered too, but on their own
// per-index sequence, so they say nothing about a chain's order.
var journalExts = []string{snapshotExt, xlogExt}

// ApplyOpts are the parameters of tt restore apply.
type ApplyOpts struct {
	// Archives is the backup chain in order: the full backup first, then
	// each increment.
	Archives []string
	// Checksums are the sha256 of each archive, in the same order. Empty
	// skips the check.
	Checksums []string
	// Layout is where each kind of file is put. All three directories have
	// to be named; FlatLayout is the one directory case.
	Layout Layout
	// WorkDir is what the caller called the restore's base directory. It is
	// recorded in the marker and nothing else: the data goes where Layout
	// says. Empty records the snapshot directory instead.
	WorkDir string
	// InstanceName is the instance the directories were resolved for, empty
	// when they were given directly. It is recorded in the marker.
	InstanceName string
	// Point is where the final xlog is cut. Nil replays the chain whole.
	Point *Point
	// PointName is the cluster recovery point the position came from; it is
	// recorded in the marker and not otherwise used.
	PointName string
	// PatchUUID is the instance UUID to stamp into every header. Empty
	// leaves the headers as they are.
	PatchUUID string
	// Config is what the cluster configuration says about the instance the
	// directories were resolved for, nil when they were given directly. With
	// it, restored data the instance would refuse to start on is refused;
	// without it, the same finding is a warning.
	Config *ConfigIdentity
}

// ApplyResult reports what a run produced.
type ApplyResult struct {
	// Files are the entry names landed, in the order they were unpacked and
	// relative to the directory of their own kind -- Layout.DirFor says which
	// one that is. Flat for snap/xlog, but vinyl's .run/.index carry a
	// <space_id>/<index_id>/ prefix.
	Files []string
	// Patched counts the headers restamped with the new instance UUID.
	Patched int
	// TrimmedFile is the base name of the xlog cut at the point, empty when
	// nothing was cut.
	TrimmedFile string
	// DroppedFiles are the base names removed for starting past the point.
	DroppedFiles []string
	// StatePath is where the marker was written.
	StatePath string
}

// Apply rebuilds the layout's directories from the archive chain: it verifies
// the inputs, clears the previous attempt, unpacks the chain in order --
// routing every file into the directory of its kind -- stamps the instance
// UUID into every header, cuts the final xlog at the recovery point, and
// leaves a restore_state.json marker beside the snapshot directory. Archives
// that do not continue one another are refused rather than applied.
//
// It is idempotent: a re-run for the same point clears what the last one left
// and repeats the work. Stopping the instance beforehand is the caller's job;
// Apply neither checks for a running instance nor starts one afterwards.
//
// The UUID stamped into the headers is verified against the _cluster the
// restored snapshot carries, and the snapshot against what the instance's
// configuration needs to start on it, before the marker claims a completed
// restore.
func Apply(opts ApplyOpts) (*ApplyResult, error) {
	err := validate(opts)
	if err != nil {
		return nil, err
	}

	// Resolved once, here, and used for everything that follows: routing,
	// cleanup, the trim, the marker's path and the directories it records. A
	// directory named relative to the process's working directory names the
	// same place, but only while that working directory is what it was, and the
	// marker outlives the process that wrote it.
	layout := opts.Layout.Resolved()

	// Every rejection happens before the work directory is touched, so a
	// rejected input leaves the previous attempt intact -- which is what an
	// orchestrator retrying on that exit code relies on. Saying anything about
	// the archives means reading them through, so they are read twice: once
	// here for their entry names and manifest fragments, and once again to
	// unpack. The first pass writes nothing.
	err = verifyChecksums(opts.Archives, opts.Checksums)
	if err != nil {
		return nil, err
	}

	contents, err := inspectChain(opts.Archives)
	if err != nil {
		return nil, err
	}

	err = checkChain(contents)
	if err != nil {
		return nil, err
	}

	err = prepareLayout(layout)
	if err != nil {
		return nil, err
	}

	result, err := unpackChain(opts, layout)
	if err != nil {
		return nil, err
	}

	// The journals on disk must continue one another, and the check has to see
	// the untrimmed chain: a hole is a property of the archives, not of where
	// the point sits. Like the UUID check below, it is a plain failure -- the
	// directories have been rebuilt by the time it runs, so no exit code
	// promises anything about a previous attempt.
	err = checkJournalContinuity(layout)
	if err != nil {
		return nil, err
	}

	if opts.Point != nil {
		trimmed, dropped, err := trimAtPoint(layout, *opts.Point)
		if err != nil {
			return nil, err
		}

		result.TrimmedFile = trimmed
		result.DroppedFiles = dropped
		result.Files = removeNames(result.Files, dropped)
	}

	// The trim decides which snapshot the instance boots on, so the check
	// runs after it: the membership to verify against is the one that
	// survives. It is a plain failure rather than a rejected input -- the
	// directories have been rebuilt by now, so there is no previous attempt
	// left whose exit code would promise one was kept intact.
	if opts.PatchUUID != "" {
		err := checkPatchUUID(layout, opts.PatchUUID, opts.InstanceName)
		if err != nil {
			return nil, err
		}
	}

	// Membership is not enough to start on: Tarantool's configuration looks
	// for the instance's name in the snapshot alone, before any journal is
	// replayed. Checked whether or not the headers were patched -- the
	// snapshot's own UUID is the one it looks up either way.
	err = checkStartupNames(layout, opts)
	if err != nil {
		return nil, err
	}

	// The three directories are recorded absolute because the marker is read
	// somewhere else: by an orchestrator comparing the nodes of a cluster, and
	// by an operator working out what a node was restored from. WorkDir is
	// recorded as the cleaned form of what the caller passed and stays relative
	// when the caller's spelling was: nothing is resolved against it, and the
	// field is there to say which call produced the marker.
	recordedWorkDir := opts.WorkDir
	if recordedWorkDir == "" {
		recordedWorkDir = layout.Snapshot
	}

	state := &State{
		SchemaVersion: StateSchemaVersion,
		WorkDir:       filepath.Clean(recordedWorkDir),
		SnapshotDir:   layout.Snapshot,
		WALDir:        layout.WAL,
		VinylDir:      layout.Vinyl,
		InstanceName:  opts.InstanceName,
		PointName:     opts.PointName,
		TargetPoint:   opts.Point,
		InstanceUUID:  opts.PatchUUID,
		Archives:      baseNames(opts.Archives),
		AppliedAt:     time.Now().UTC(),
	}

	result.StatePath = StatePath(layout.Snapshot)

	err = writeState(result.StatePath, state)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// validate checks the inputs that cost nothing to check.
func validate(opts ApplyOpts) error {
	if len(opts.Archives) == 0 {
		return fmt.Errorf("%w: no archives given", ErrValidation)
	}

	for _, dir := range []struct {
		kind  string
		value string
	}{
		{kind: "snapshot", value: opts.Layout.Snapshot},
		{kind: "wal", value: opts.Layout.WAL},
		{kind: "vinyl", value: opts.Layout.Vinyl},
	} {
		if dir.value == "" {
			return fmt.Errorf("%w: no %s directory given", ErrValidation, dir.kind)
		}
	}

	if len(opts.Checksums) != 0 && len(opts.Checksums) != len(opts.Archives) {
		return fmt.Errorf("%w: %d checksums for %d archives",
			ErrValidation, len(opts.Checksums), len(opts.Archives))
	}

	// Parsed here rather than where it is stamped in: the headers are patched
	// after the work directory has been cleared and the first archive
	// unpacked, so a UUID rejected there would take the previous attempt with
	// it and report a plain failure instead of a rejected input.
	if opts.PatchUUID != "" {
		_, err := uuid.Parse(opts.PatchUUID)
		if err != nil {
			return fmt.Errorf("%w: --patch-uuid %q: %w", ErrValidation, opts.PatchUUID, err)
		}
	}

	for _, path := range opts.Archives {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("%w: archive %q: %w", ErrValidation, path, err)
		}

		if info.IsDir() {
			return fmt.Errorf("%w: archive %q is a directory", ErrValidation, path)
		}
	}

	return nil
}

// verifyChecksums recomputes the sha256 of every archive. The archives were
// already checked once, on the manager host, before they were handed out;
// this catches a copy to the node that went wrong afterwards.
func verifyChecksums(archives, checksums []string) error {
	if len(checksums) == 0 {
		return nil
	}

	for itemIndex, path := range archives {
		got, err := archive.Checksum(path)
		if err != nil {
			return fmt.Errorf("failed to checksum archive %q: %w", path, err)
		}

		if !strings.EqualFold(got, checksums[itemIndex]) {
			return fmt.Errorf("%w: archive %q has checksum %s, expected %s",
				ErrValidation, path, got, checksums[itemIndex])
		}
	}

	return nil
}

// prepareLayout drops the marker of a previous run and then the files that run
// left in each of the layout's directories, so what follows starts from clean
// ones.
//
// The layout's own directories are kept through the sweep even when it empties
// them. A restore is given the directories an instance is configured with, and
// one of them can be a mount point or a directory an operator created with a
// mode and an owner of their own; removing and recreating it would answer with
// a fresh directory owned by whoever ran the restore, and on a mount point the
// removal fails outright with the files already gone.
func prepareLayout(layout Layout) error {
	dirs := layout.dataDirs()

	err := makeDataDirs(dirs)
	if err != nil {
		return err
	}

	keep, err := statDirs(dirs)
	if err != nil {
		return err
	}

	// The marker goes first: between here and the end of the run the
	// directories are incomplete, and nothing should claim otherwise.
	err = removeState(layout.Snapshot)
	if err != nil {
		return err
	}

	for _, dir := range dirs {
		err := removeRestoreArtifacts(dir, keep)
		if err != nil {
			return err
		}
	}

	return nil
}

// statDirs stats every directory of a layout, so that the sweep can recognize
// one of them by what the filesystem says it is rather than by the name it was
// reached under.
func statDirs(dirs []string) ([]os.FileInfo, error) {
	infos := make([]os.FileInfo, 0, len(dirs))

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("failed to read work directory %q: %w", dir, err)
		}

		infos = append(infos, info)
	}

	return infos, nil
}

// makeDataDirs creates every directory of a layout.
func makeDataDirs(dirs []string) error {
	for _, dir := range dirs {
		err := os.MkdirAll(dir, restoredDirMode)
		if err != nil {
			return fmt.Errorf("failed to create work directory %q: %w", dir, err)
		}
	}

	return nil
}

// removeRestoreArtifacts recursively removes every file cleanup owns
// (isRestoreArtifact) under dir, at any depth, and prunes directories it
// leaves empty behind it. Depth matters here: vinyl's .run/.index files from
// a previous attempt nest under <space_id>/<index_id>/, and a run that only
// swept the top level would leave them for the next unpack to land beside,
// stale data a fresh instance would replay right along with the real thing.
// Files cleanup does not own, wherever they live, are left untouched, and so
// is any directory that still holds one.
//
// keep is what the filesystem says each of the layout's own directories is.
// They are emptied like any other directory and then left standing, whichever
// name the sweep reaches them under: they were named by the caller, and a
// restore is not entitled to replace one with a directory of its own making.
//
// The comparison is by identity rather than by path because a layout's
// directories are made absolute and cleaned but not resolved through symbolic
// links: with the snapshots in /srv/alias, a symlink to /srv/data, and the
// vinyl data in /srv/data/vinyl, the sweep of the first meets the second as
// /srv/alias/vinyl -- a name no directory of the layout carries, and the one
// directory under it that must survive.
func removeRestoreArtifacts(dir string, keep []os.FileInfo) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())

		if entry.IsDir() { //nolint:nestif // Preserve data roots when pruning directories.
			err := removeRestoreArtifacts(path, keep)
			if err != nil {
				return fmt.Errorf("failed to remove restore artifacts from %q: %w", path, err)
			}

			kept, err := isKeptDir(path, keep)
			if err != nil {
				return err
			}

			if kept {
				continue
			}

			empty, err := isEmptyDir(path)
			if err != nil {
				return fmt.Errorf("failed to check if directory %q is empty: %w", path, err)
			}

			if empty {
				err := os.Remove(path)
				if err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("failed to remove empty directory %q: %w", path, err)
				}
			}

			continue
		}

		if !isRestoreArtifact(entry.Name()) {
			continue
		}

		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove stale file %q: %w", path, err)
		}
	}

	return nil
}

// isKeptDir reports whether path is one of the layout's own directories,
// reached under whatever name the sweep arrived at it by.
func isKeptDir(path string, keep []os.FileInfo) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to read directory %q: %w", path, err)
	}

	for _, kept := range keep {
		if os.SameFile(info, kept) {
			return true, nil
		}
	}

	return false, nil
}

// isEmptyDir reports whether dir holds no entries.
func isEmptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	return len(entries) == 0, nil
}

// isRestoreArtifact reports whether cleanup owns this file. The name has to be
// longer than the extension: a dotfile called ".snap" is somebody's own file,
// not a snapshot, and cleanup is not entitled to it. util.hasExt draws the
// line in the same place.
func isRestoreArtifact(name string) bool {
	for _, ext := range restoreArtifactExts {
		if strings.HasSuffix(name, ext) && len(name) > len(ext) {
			return true
		}
	}

	return false
}

// inspectChain reads every archive through without unpacking it, so that an
// archive naming an entry the restore cannot place, and a chain whose archives
// do not continue one another, are both refused while the directories still
// hold whatever the last run left there.
//
// It costs a full zstd decode of every archive given, which is the price of a
// rejection that arrives before the previous attempt is cleared -- the promise
// an orchestrator reads out of that exit code.
func inspectChain(paths []string) ([]archiveContent, error) {
	contents := make([]archiveContent, 0, len(paths))

	for _, path := range paths {
		content, err := inspectArchive(path)
		if err != nil {
			return nil, err
		}

		contents = append(contents, content)
	}

	return contents, nil
}

// checkChain refuses a chain whose archives do not continue one another. A
// single archive is no chain and nothing is compared.
func checkChain(contents []archiveContent) error {
	for itemIndex := 1; itemIndex < len(contents); itemIndex++ {
		err := checkChainOrder(contents[itemIndex-1], contents[itemIndex])
		if err != nil {
			return err
		}
	}

	return nil
}

// inspectArchive reads one archive for what the checks need -- the names it
// would land and the manifest fragment describing the backup -- without
// writing anything. Entry bodies are left unread: the tar reader skips over
// the ones nobody consumed.
func inspectArchive(src string) (archiveContent, error) {
	content := archiveContent{path: src, files: nil, fragment: nil}

	// Two entries of one archive reducing to one name are refused. Unpacking
	// truncates what it writes, so the second would replace the first under a
	// name that then stands for whichever of the two came last, and the report
	// -- which counts a name once -- would say nothing about the file that is
	// missing. Archives of a chain naming the same file is another matter
	// entirely: they overlap on the boundary journal by construction, and the
	// later copy is the longer one.
	named := make(map[string]string)

	for entry, err := range archive.Entries(src) {
		if err != nil {
			return archiveContent{}, readArchiveError(src, err)
		}

		if entry.Name == fragmentEntryName {
			content.fragment = readFragment(entry.Body)

			continue
		}

		name, err := canonicalEntryName(entry.Name)
		if err != nil {
			return archiveContent{}, fmt.Errorf("archive %q: %w", src, err)
		}

		previous, taken := named[name]

		if taken {
			return archiveContent{}, fmt.Errorf(
				"%w: archive %q names both %q and %q, and both are restored as %q",
				ErrValidation, src, previous, entry.Name, name,
			)
		}

		named[name] = entry.Name

		content.files = append(content.files, name)
	}

	return content, nil
}

// readArchiveError says what a failure to read an archive was. A name the
// archive package itself refuses is the archive naming a file that cannot be
// placed, which is a rejected input like every other name that cannot be
// placed: the caller is told to correct the call, and the previous attempt is
// left where it is.
func readArchiveError(src string, err error) error {
	if errors.Is(err, archive.ErrUnsafeEntryName) {
		return fmt.Errorf("%w: archive %q: %w", ErrValidation, src, err)
	}

	return fmt.Errorf("failed to read archive %q: %w", src, err)
}

// unpackChain writes the archives into the layout in the order they were
// given, stamping the instance UUID into every header that lands, and reports
// what the directories ended up holding. The order itself has already been
// checked, by checkChain, before the directories were cleared.
func unpackChain(opts ApplyOpts, layout Layout) (*ApplyResult, error) {
	result := new(ApplyResult)
	landed := make(map[string]struct{})

	for _, path := range opts.Archives {
		content, err := unpackArchive(path, layout)
		if err != nil {
			return nil, err
		}

		for _, name := range content.files {
			patched, err := patchHeader(
				entryPath(layout, name),
				opts.PatchUUID,
			)
			if err != nil {
				return nil, err
			}

			// The archives of a chain overlap on the boundary journal, so the
			// same name lands more than once. The report counts the files the
			// work directory holds, not the times one was written into it.
			_, seen := landed[name]

			if seen {
				continue
			}

			landed[name] = struct{}{}
			result.Files = append(result.Files, name)

			if patched {
				result.Patched++
			}
		}
	}

	return result, nil
}

// checkPatchUUID refuses a --patch-uuid the restored replicaset does not
// know at the recovery point, and one bound to another instance than the one
// the directories were resolved for. The headers have already been stamped
// with it: Tarantool checks every file's header against the instance it
// recovers, and a UUID the restored _cluster does not carry leaves the node
// unable to register, which is a divergence the marker would otherwise
// certify as a completed restore.
//
// The membership is the one the replay ends with, not the snapshot's alone:
// a chain registers and forgets members after the snapshot was taken, so the
// journals that follow it are folded in -- already trimmed to the point by
// the time this runs. A chain that restored no snapshot has no membership to
// verify against, so the check warns and steps aside rather than refusing.
//
// The name binding is demanded only of the member the uuid matched: a member
// the replicaset registered without a name -- of a named replicaset or an
// unnamed one -- has nothing to contradict the caller with, and the plain
// presence of the UUID is everything the backup can vouch for.
//
// Membership is what the replay ends with, and it is not what the instance
// starts by: whether Tarantool finds the instance's name where it looks for
// it, in the snapshot alone, is checkStartupNames's question.
//
//nolint:err113 // Preserve established restore error messages.
func checkPatchUUID(layout Layout, patchUUID, instanceName string) error {
	snapshot, ok, err := newestSnapshot(layout.Snapshot)
	if err != nil {
		return fmt.Errorf("failed to list the snapshots restored into %q: %w",
			layout.Snapshot, err)
	}

	if !ok {
		log.Warnf("no snapshot in the restored chain: --patch-uuid %s is not "+
			"checked against the restored _cluster", patchUUID)

		return nil
	}

	journals, err := sortedJournals(layout.WAL)
	if err != nil {
		return err
	}

	members, err := xlog.MembershipAt(snapshot, journals)
	if err != nil {
		return err
	}

	// Parsed rather than compared as strings: a --patch-uuid the caller
	// spelled in capitals is the same UUID as the one the snapshot holds.
	patchID, err := uuid.Parse(patchUUID)
	if err != nil {
		return fmt.Errorf("failed to parse --patch-uuid %q: %w", patchUUID, err)
	}

	matched := -1

	for itemIndex, member := range members {
		memberID, err := uuid.Parse(member.UUID)
		if err != nil {
			return fmt.Errorf("the restored _cluster carries a malformed uuid %q: %w",
				member.UUID, err)
		}

		if memberID == patchID {
			matched = itemIndex
		}
	}

	if matched < 0 {
		return fmt.Errorf("--patch-uuid %s is not a member of the restored "+
			"replicaset: _cluster of %s holds %s", patchUUID, snapshot,
			describeCluster(members))
	}

	// The name binding is demanded only of the member the uuid matched -- see
	// the function comment -- never of the membership as a whole.
	if instanceName == "" || members[matched].Name == "" {
		return nil
	}

	if members[matched].Name != instanceName {
		return fmt.Errorf("--patch-uuid %s is not bound to instance %q in the "+
			"restored _cluster: it holds %s", patchUUID, instanceName,
			describeCluster(members))
	}

	return nil
}

// sortedJournals names the journal files of dir in replay order, which is
// the order of their vclock signatures: the name of a journal is its
// signature, and a directory listing comes out sorted by name.
func sortedJournals(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to list the journals restored into %q: %w", dir, err)
	}

	journals := make([]string, 0, len(entries))
	for _, entry := range entries {
		// A dotfile is not a journal however its name ends -- cleanup keeps
		// them on purpose, and a directory holding one must not cost the
		// check its journals.
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		if filepath.Ext(entry.Name()) == xlogExt {
			journals = append(journals, filepath.Join(dir, entry.Name()))
		}
	}

	return journals, nil
}

// checkJournalContinuity refuses a chain whose unpacked journals do not
// continue one another. Walking the snapshot and the journals in replay
// order, every journal after the first must begin exactly where the previous
// one's content ends, and the first one must begin no later than the snapshot
// ends: a gap means an increment of the chain is missing -- a hole
// the archive-level checks cannot see, because real archives carry no
// manifest fragment and journal names stay monotone across it -- and an
// overlap means two files claim the same rows.
//
// A journal begins at the position its header carries, the one its name
// encodes, and its content ends at the fold of the rows it holds. A snapshot
// is the state at the position its header names, so the header is what its
// end is taken from. The first journal of a chain may begin before the
// snapshot ends -- the boundary journal carries rows from both sides of the
// checkpoint -- but never past it. The comparison is exact, component 0
// included, which is the continuity rule Tarantool's own recovery applies
// to a pair of WALs (recovery_open_log, src/box/recovery.cc).
func checkJournalContinuity(layout Layout) error {
	snapshot, hasSnapshot, err := newestSnapshot(layout.Snapshot)
	if err != nil {
		return fmt.Errorf("failed to list the snapshots restored into %q: %w",
			layout.Snapshot, err)
	}

	journals, err := sortedJournals(layout.WAL)
	if err != nil {
		return err
	}

	if !hasSnapshot && len(journals) == 0 {
		return nil
	}

	end, endName := format.VClock{}, ""

	if hasSnapshot {
		meta, err := reader.ReadHeader(snapshot)
		if err != nil {
			return fmt.Errorf("failed to read the header of %q: %w", snapshot, err)
		}

		end, endName = meta.VClock, filepath.Base(snapshot)
	}

	for itemIndex, journal := range journals {
		start, contentEnd, err := journalPositions(journal)
		if err != nil {
			return err
		}

		name := filepath.Base(journal)

		switch {
		case itemIndex == 0 && hasSnapshot:
			// The boundary journal normally begins before the snapshot ends --
			// it carries rows from both sides of the checkpoint -- so only a
			// beginning past the snapshot's end is a gap here.
			if start.Signature() > end.Signature() {
				return journalGapError(name, endName, end, start)
			}
		case itemIndex > 0:
			err := checkJournalOrder(end, endName, name, start)
			if err != nil {
				return err
			}
		}

		end, endName = contentEnd, name
	}

	return nil
}

// journalPositions returns where a journal begins -- the vclock its header
// names, the one its file name encodes -- and where its content ends: the
// beginning promoted by every row it carries, so a journal with no rows ends
// where it begins.
func journalPositions(path string) (format.VClock, format.VClock, error) {
	meta, err := reader.ReadHeader(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read the header of %q: %w", path, err)
	}

	journalReader, err := reader.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read %q: %w", path, err)
	}

	defer func() { _ = journalReader.Close() }()

	end := meta.VClock.Clone()

	for row, rowErr := range journalReader.Rows() {
		if rowErr != nil {
			return nil, nil, fmt.Errorf("failed to read %q: %w", path, rowErr)
		}

		// The rows of one replica sit in a journal in LSN order, so the last
		// one seen is the position the content ends at; axis 0 carries the
		// local rows the same way.
		end[row.ReplicaID] = row.LSN
	}

	return meta.VClock, end, nil
}

// checkJournalOrder refuses a journal that does not begin exactly where the
// previous journal of the chain ends: past it is a gap -- the rows in between
// are in a file that was never given -- and behind it is an overlap, two
// files claiming the same rows.
//
//nolint:err113 // Preserve established restore error messages.
func checkJournalOrder(end format.VClock, endName, nextName string, begin format.VClock) error {
	if maps.Equal(end, begin) {
		return nil
	}

	if begin.Signature() > end.Signature() {
		return journalGapError(nextName, endName, end, begin)
	}

	return fmt.Errorf("journal %s overlaps %s: the chain ends at %s, and %s begins at %s",
		nextName, endName, end, nextName, begin)
}

// journalGapError names a gap: the file the chain ends at, where its content
// ends, and the file that jumps past it.
//
//nolint:err113 // Preserve established restore error messages.
func journalGapError(nextName, endName string, end, begin format.VClock) error {
	return fmt.Errorf("journal %s does not continue %s: the chain ends at %s, "+
		"and %s begins at %s", nextName, endName, end, nextName, begin)
}

// snapshotExt is the extension of the files an instance can boot from: the
// ones a restored layout is checked against.
const snapshotExt = ".snap"

// newestSnapshot is the .snap a node recovered into the layout boots on: the
// one with the highest vclock signature in the snapshot directory. Tarantool
// and the trim both index a journal by that signature, so a name that carries
// no parseable one is somebody else's file and never the boot snapshot.
func newestSnapshot(dir string) (string, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	newest, newestSignature := "", int64(-1)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != snapshotExt {
			continue
		}

		signature, ok := journalSignature(entry.Name())
		if !ok || signature <= newestSignature {
			continue
		}

		newest, newestSignature = filepath.Join(dir, entry.Name()), signature
	}

	return newest, newest != "", nil
}

// describeCluster renders a membership for a refusal message: one entry per
// member, id, UUID and the name when it carries one, so the operator can see
// both what was asked for and everything there was to give.
func describeCluster(members []xlog.ClusterMember) string {
	entries := make([]string, 0, len(members))

	for _, member := range members {
		entry := fmt.Sprintf("%d %s", member.ID, member.UUID)
		if member.Name != "" {
			entry = fmt.Sprintf("%s %q", entry, member.Name)
		}

		entries = append(entries, entry)
	}

	return "[" + strings.Join(entries, ", ") + "]"
}

// archiveContent is what one archive of the chain carried: the names it landed
// in the work directory, and the manifest fragment describing the backup it
// was cut from.
type archiveContent struct {
	path     string
	files    []string
	fragment *backup.Fragment
}

// entryPath is where an entry lands: under the directory of its own kind, at
// the canonical name it was reduced to.
func entryPath(layout Layout, entryName string) string {
	return filepath.Join(layout.DirFor(entryName), filepath.FromSlash(entryName))
}

// unpackArchive streams one archive into the layout and returns what it holds.
// Entries go straight to their final path: there is no staging directory, so a
// chain is never written twice.
func unpackArchive(src string, layout Layout) (archiveContent, error) {
	content := archiveContent{path: src, files: nil, fragment: nil}

	for entry, err := range archive.Entries(src) {
		if err != nil {
			return archiveContent{}, readArchiveError(src, err)
		}

		if entry.Name == fragmentEntryName {
			content.fragment = readFragment(entry.Body)

			continue
		}

		name, err := canonicalEntryName(entry.Name)
		if err != nil {
			return archiveContent{}, fmt.Errorf("archive %q: %w", src, err)
		}

		err = writeEntry(entryPath(layout, name), entry.Body)
		if err != nil {
			return archiveContent{}, fmt.Errorf("failed to unpack %q from %q: %w",
				entry.Name, src, err)
		}

		content.files = append(content.files, name)
	}

	return content, nil
}

// readFragment decodes an archive's manifest fragment, reporting one it cannot
// make sense of as absent rather than as a failure: the fragment describes the
// backup, not the instance state, and an archive packed by hand or by an older
// backup carries a stub or nothing at all. The chain check falls back on the
// journal names for those.
func readFragment(body io.Reader) *backup.Fragment {
	data, err := io.ReadAll(io.LimitReader(body, fragmentSizeLimit))
	if err != nil {
		return nil
	}

	fragment, err := backup.DecodeFragment(data)
	if err != nil {
		return nil
	}

	return fragment
}

// checkChainOrder refuses a pair of archives that do not continue one another.
//
// The chain is unpacked in the order it was given and the last write wins, so
// an increment applied before the full backup it continues ends with the
// shared boundary journal on the shorter copy: the instance boots healthy on a
// state that stops short of the point it was restored to, and the marker it
// leaves is indistinguishable from the one every other node wrote -- exactly
// the divergence the marker exists to catch.
//
// The manifest fragment decides whenever both archives carry one; the journal
// names are what is left when they do not.
func checkChainOrder(previous, next archiveContent) error {
	err := checkFragments(previous, next)
	if err != nil {
		return err
	}

	return checkJournalNames(previous, next)
}

// checkFragments compares what the two archives say about themselves: the
// instance they were taken on, and the journal range they cover.
func checkFragments(previous, next archiveContent) error {
	if previous.fragment == nil || next.fragment == nil {
		return nil
	}

	sameInstance := previous.fragment.ReplicasetUUID == next.fragment.ReplicasetUUID &&
		previous.fragment.InstanceUUID == next.fragment.InstanceUUID

	if !sameInstance {
		return fmt.Errorf("%w: archive %q was taken on instance %s of replicaset %s and "+
			"archive %q on instance %s of replicaset %s: a chain is one instance's",
			ErrValidation,
			previous.path, previous.fragment.InstanceUUID, previous.fragment.ReplicasetUUID,
			next.path, next.fragment.InstanceUUID, next.fragment.ReplicasetUUID)
	}

	previousEnd := vclockSignature(previous.fragment.VclockEnd)

	end := vclockSignature(next.fragment.VclockEnd)

	if end < previousEnd {
		return fmt.Errorf("%w: archive %q ends at %d, before %q ends at %d: give the full "+
			"backup first, then every increment in the order it was taken",
			ErrValidation, next.path, end, previous.path, previousEnd)
	}

	begin := vclockSignature(next.fragment.VclockBegin)

	if begin > previousEnd {
		return fmt.Errorf("%w: archive %q begins at %d, past the end of %q at %d: "+
			"an increment of the chain is missing",
			ErrValidation, next.path, begin, previous.path, previousEnd)
	}

	return nil
}

// checkJournalNames compares the last journal of each archive. A journal is
// named after the vclock signature it starts at, so an archive whose journals
// all sort below the previous archive's cannot be continuing it.
//
// It says nothing about archives holding the same journal names, which is what
// a chain taken between two WAL rotations looks like: there the fragments are
// the only thing that can tell the two apart.
func checkJournalNames(previous, next archiveContent) error {
	previousTop, ok := topJournal(previous.files)
	if !ok {
		return nil
	}

	nextTop, ok := topJournal(next.files)
	if !ok || nextTop >= previousTop {
		return nil
	}

	return fmt.Errorf("%w: archive %q holds no journal past %020d while %q reaches %020d: "+
		"give the full backup first, then every increment in the order it was taken",
		ErrValidation, next.path, nextTop, previous.path, previousTop)
}

// topJournal returns the highest vclock signature the names carry, and false
// when none of them is a journal's.
func topJournal(names []string) (int64, bool) {
	top, found := int64(0), false

	for _, name := range names {
		signature, ok := journalSignature(name)
		if !ok {
			continue
		}

		if !found || signature > top {
			top, found = signature, true
		}
	}

	return top, found
}

// journalSignature reads the position out of a journal file name -- the
// zero-padded <signature>.<ext> convention Tarantool, dir.OpenDir and this
// command's own trim all index by.
func journalSignature(name string) (int64, bool) {
	ext := filepath.Ext(name)
	if !slices.Contains(journalExts, ext) {
		return 0, false
	}

	signature, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(name), ext), 10, 64)
	if err != nil {
		return 0, false
	}

	return signature, true
}

// vclockSignature is a vclock's position on a single axis: the sum of its
// LSNs, which is what journal files are named after.
func vclockSignature(vclock backup.Vclock) uint64 {
	var signature uint64

	for _, lsn := range vclock {
		signature += lsn
	}

	return signature
}

// writeEntry copies one archive entry to path.
func writeEntry(path string, body io.Reader) error {
	err := os.MkdirAll(filepath.Dir(path), restoredDirMode)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, restoredFileMode)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	_, err = io.Copy(out, body)
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("failed to write content: %w", err)
	}

	err = out.Close()
	if err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	return nil
}

// patchHeader stamps newUUID into a landed snap/xlog, reporting whether it
// touched the file. The UUID a restored node must own is the one its own
// `_cluster` records, so the stamp confirms the headers when the archives are
// replayed onto the instance they were taken on, and rewrites them when they
// are replayed onto another one. Every file has to agree: Tarantool checks the
// `.vylog` at startup and refuses an instance whose UUID it does not share.
func patchHeader(path, newUUID string) (bool, error) {
	if newUUID == "" {
		return false, nil
	}

	if !slices.Contains(patchedExts, filepath.Ext(path)) {
		return false, nil
	}

	err := xlog.PatchInstanceUUID(path, path, newUUID)
	if err == nil {
		return true, nil
	}

	if !errors.Is(err, xlog.ErrInPlaceWidthMismatch) {
		return false, fmt.Errorf("failed to patch instance uuid in %q: %w", path, err)
	}

	// The header holds a UUID of some other width, so overwriting it in
	// place would shift every following byte. Rewrite through a copy.
	tmp := path + patchTmpSuffix

	err = xlog.PatchInstanceUUID(path, tmp, newUUID)
	if err != nil {
		return false, fmt.Errorf("failed to patch instance uuid in %q: %w", path, err)
	}

	err = os.Rename(tmp, path)
	if err != nil {
		return false, fmt.Errorf("failed to replace %q with its patched copy: %w", path, err)
	}

	return true, nil
}

// trimAtPoint cuts the chain down to the recovery point and returns the base
// name of the xlog it cut, plus the base names it dropped whole.
//
// The trimmed file replaces the original under its own name: a journal file
// is indexed by the vclock signature in its name, so it has to stay where it
// was.
func trimAtPoint(layout Layout, point Point) (string, []string, error) {
	// The command parses the recovery point before passing it to Apply.
	lsn := int64(point.LSN) //nolint:gosec // Preserve the original signed LSN conversion.

	src, err := xlog.FindTrimFile(layout.WAL, point.ReplicaID, lsn)
	if err != nil {
		if errors.Is(err, xlog.ErrTrimFileNotFound) {
			return "", nil, fmt.Errorf("%w: %s in %q", ErrNoTrimFile, point, layout.WAL)
		}

		return "", nil, fmt.Errorf("failed to locate the xlog to trim: %w", err)
	}

	// A backup covers a range, and the point sits somewhere inside it, so the
	// chain usually continues past the file being cut. Those files have to go:
	// Tarantool replays every journal it finds, so leaving them would carry
	// the instance straight through the point it was restored to — and the
	// result looks healthy, it is simply the wrong state.
	dropped, err := dropJournalsAfter(layout, src)
	if err != nil {
		return "", nil, err
	}

	tmp := src + trimTmpSuffix

	err = xlog.TruncateAt(src, tmp, point.ReplicaID, lsn)
	if err != nil {
		return "", nil, fmt.Errorf("failed to trim %q at %s: %w", src, point, err)
	}

	err = os.Rename(tmp, src)
	if err != nil {
		return "", nil, fmt.Errorf("failed to replace %q with its trimmed copy: %w", src, err)
	}

	return filepath.Base(src), dropped, nil
}

// dropJournalsAfter removes the snapshots and xlogs that start past the file
// holding the point, and returns their base names. Each kind is looked for in
// its own directory, because a snapshot past the point carries the instance
// beyond it exactly as an xlog does and the two need not live together. It
// runs before the trim so a directory is only ever indexed while every file
// still matches its name.
//
// A snapshot's sort data goes with the snapshot it belongs to: it is named
// after the same signature and describes that snapshot's indexes, so one left
// behind describes a file that is no longer there.
func dropJournalsAfter(layout Layout, trimFile string) ([]string, error) {
	signature, err := xlog.SignatureOf(trimFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read the position of %q: %w", trimFile, err)
	}

	paths, err := xlog.JournalsAfter(layout.Snapshot, layout.WAL, signature)
	if err != nil {
		return nil, fmt.Errorf("failed to list the files past the recovery point: %w", err)
	}

	sortData, err := sortDataAfter(layout.Snapshot, signature)
	if err != nil {
		return nil, err
	}

	paths = append(paths, sortData...)

	dropped := make([]string, 0, len(paths))

	for _, path := range paths {
		err := os.Remove(path)
		if err != nil {
			return nil, fmt.Errorf("failed to remove %q: %w", path, err)
		}

		dropped = append(dropped, filepath.Base(path))
	}

	return dropped, nil
}

// sortDataAfter returns the memtx sort data files in dir whose snapshot starts
// past signature. Tarantool names <signature>.sortdata after the snapshot it
// was written beside, which is what lets it be matched against a recovery
// point without being read; it is not a journal, and nothing about a chain's
// order is decided by it.
func sortDataAfter(dir string, signature int64) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	var after []string

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() || filepath.Ext(name) != sortDataExt {
			continue
		}

		// A name that is not a signature is somebody else's file, and cleanup
		// is the only thing entitled to it.
		position, err := strconv.ParseInt(strings.TrimSuffix(name, sortDataExt), 10, 64)
		if err != nil {
			continue
		}

		if position > signature {
			after = append(after, filepath.Join(dir, name))
		}
	}

	return after, nil
}

// removeNames returns names without the entries listed in drop.
func removeNames(names, drop []string) []string {
	if len(drop) == 0 {
		return names
	}

	dropped := make(map[string]struct{}, len(drop))
	for _, name := range drop {
		dropped[name] = struct{}{}
	}

	kept := names[:0]

	for _, name := range names {
		_, ok := dropped[name]

		if !ok {
			kept = append(kept, name)
		}
	}

	return kept
}

// baseNames strips the directories off a list of paths.
func baseNames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}

	return names
}
