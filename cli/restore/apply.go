// Package restore prepares a node's work directory from backup archives so
// Tarantool can start on a chosen recovery point.
package restore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/archive"
	"github.com/tarantool/tt/cli/backup/xlog"
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

// restoreArtifactExts are the files a restore owns in the work directory:
// everything Tarantool replays, plus the half-finished files this command and
// go-xlog leave behind when they are interrupted. Cleanup removes exactly
// these, so a work directory holding an instance's config or logs keeps them.
var restoreArtifactExts = []string{
	".snap", ".xlog", ".vylog", ".run", ".index",
	".inprogress", ".uuidbak", patchTmpSuffix, trimTmpSuffix,
}

// patchedExts are the files whose header carries an instance UUID. Every one
// of them is checked at startup, not just the journals: a .vylog left on the
// backed-up master's UUID fails recovery outright with "invalid instance
// UUID", and the vinyl run/index files carry the same field.
var patchedExts = []string{".snap", ".xlog", ".vylog", ".run", ".index"}

// journalExts are the files Tarantool names after the vclock signature they
// start at. Vinyl's .run and .index are numbered too, but on their own
// per-index sequence, so they say nothing about a chain's order.
var journalExts = []string{".snap", ".xlog"}

// ApplyOpts are the parameters of tt restore apply.
type ApplyOpts struct {
	// Archives is the backup chain in order: the full backup first, then
	// each increment.
	Archives []string
	// Checksums are the sha256 of each archive, in the same order. Empty
	// skips the check.
	Checksums []string
	// WorkDir is the instance directory to rebuild.
	WorkDir string
	// Point is where the final xlog is cut. Nil replays the chain whole.
	Point *Point
	// PointName is the cluster recovery point the position came from; it is
	// recorded in the marker and not otherwise used.
	PointName string
	// PatchUUID is the instance UUID to stamp into every header. Empty
	// leaves the headers as they are.
	PatchUUID string
}

// ApplyResult reports what a run produced.
type ApplyResult struct {
	// Files are the base names landed in the work directory, in the order
	// they were unpacked.
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

// Apply rebuilds WorkDir from the archive chain: it verifies the inputs,
// clears the previous attempt, unpacks the chain in order, stamps the
// instance UUID into every header, cuts the final xlog at the recovery point,
// and leaves a restore_state.json marker beside the directory. Archives that
// do not continue one another are refused rather than applied.
//
// It is idempotent: a re-run for the same point clears what the last one left
// and repeats the work. Stopping the instance beforehand is the caller's job;
// Apply neither checks for a running instance nor starts one afterwards.
func Apply(opts ApplyOpts) (*ApplyResult, error) {
	if err := validate(opts); err != nil {
		return nil, err //nolint:wrapcheck
	}

	// Everything that can reject the inputs without reading the archives
	// through runs before the work directory is touched, so those rejections
	// leave the previous attempt intact. The chain check is the exception: an
	// archive only says which part of the journal it covers once its stream
	// has been read, and reading every archive twice would double the work of
	// a restore.
	if err := verifyChecksums(opts.Archives, opts.Checksums); err != nil {
		return nil, err //nolint:wrapcheck
	}

	if err := prepareWorkDir(opts.WorkDir); err != nil {
		return nil, err //nolint:wrapcheck
	}

	result, err := unpackChain(opts)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	if opts.Point != nil {
		trimmed, dropped, err := trimAtPoint(opts.WorkDir, *opts.Point)
		if err != nil {
			return nil, err //nolint:wrapcheck
		}

		result.TrimmedFile = trimmed
		result.DroppedFiles = dropped
		result.Files = removeNames(result.Files, dropped)
	}

	state := &State{
		SchemaVersion: StateSchemaVersion,
		WorkDir:       filepath.Clean(opts.WorkDir),
		PointName:     opts.PointName,
		TargetPoint:   opts.Point,
		InstanceUUID:  opts.PatchUUID,
		Archives:      baseNames(opts.Archives),
		AppliedAt:     time.Now().UTC(),
	}

	if err := writeState(state); err != nil {
		return nil, err //nolint:wrapcheck
	}

	result.StatePath = StatePath(opts.WorkDir)

	return result, nil
}

// validate checks the inputs that cost nothing to check.
func validate(opts ApplyOpts) error {
	if len(opts.Archives) == 0 {
		return fmt.Errorf("%w: no archives given", ErrValidation)
	}

	if opts.WorkDir == "" {
		return fmt.Errorf("%w: no work directory given", ErrValidation)
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
		if _, err := uuid.Parse(opts.PatchUUID); err != nil {
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

	for i, path := range archives {
		got, err := archive.Checksum(path)
		if err != nil {
			return fmt.Errorf("failed to checksum archive %q: %w", path, err)
		}

		if !strings.EqualFold(got, checksums[i]) {
			return fmt.Errorf("%w: archive %q has checksum %s, expected %s",
				ErrValidation, path, got, checksums[i])
		}
	}

	return nil
}

// prepareWorkDir drops the marker of a previous run and then the files that
// run left, so what follows starts from a clean directory.
func prepareWorkDir(workDir string) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("failed to create work directory %q: %w", workDir, err)
	}

	// The marker goes first: between here and the end of the run the
	// directory is incomplete, and nothing should claim otherwise.
	if err := removeState(workDir); err != nil {
		return err //nolint:wrapcheck
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		return fmt.Errorf("failed to read work directory %q: %w", workDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !isRestoreArtifact(entry.Name()) {
			continue
		}

		path := filepath.Join(workDir, entry.Name())
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove stale file %q: %w", path, err)
		}
	}

	return nil
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

// unpackChain writes the archives into the work directory in the order they
// were given, stamping the instance UUID into every header that lands, and
// reports what the work directory ended up holding.
func unpackChain(opts ApplyOpts) (*ApplyResult, error) {
	result := &ApplyResult{}
	landed := make(map[string]struct{})

	var previous archiveContent

	for i, path := range opts.Archives {
		content, err := unpackArchive(path, opts.WorkDir)
		if err != nil {
			return nil, err //nolint:wrapcheck
		}

		if i > 0 {
			if err := checkChainOrder(previous, content); err != nil {
				return nil, err //nolint:wrapcheck
			}
		}

		for _, name := range content.files {
			patched, err := patchHeader(filepath.Join(opts.WorkDir, name), opts.PatchUUID)
			if err != nil {
				return nil, err //nolint:wrapcheck
			}

			// The archives of a chain overlap on the boundary journal, so the
			// same name lands more than once. The report counts the files the
			// work directory holds, not the times one was written into it.
			if _, seen := landed[name]; seen {
				continue
			}

			landed[name] = struct{}{}
			result.Files = append(result.Files, name)

			if patched {
				result.Patched++
			}
		}

		previous = content
	}

	return result, nil
}

// archiveContent is what one archive of the chain carried: the names it landed
// in the work directory, and the manifest fragment describing the backup it
// was cut from.
type archiveContent struct {
	path     string
	files    []string
	fragment *backup.Fragment
}

// unpackArchive streams one archive into workDir and returns what it holds.
// Entries go straight to their final path: there is no staging directory, so a
// chain is never written twice.
func unpackArchive(src, workDir string) (archiveContent, error) {
	content := archiveContent{path: src}

	for entry, err := range archive.Entries(src) {
		if err != nil {
			return archiveContent{}, fmt.Errorf("failed to read archive %q: %w", src, err)
		}

		if entry.Name == fragmentEntryName {
			content.fragment = readFragment(entry.Body)

			continue
		}

		if err := writeEntry(filepath.Join(workDir, entry.Name), entry.Body); err != nil {
			return archiveContent{}, fmt.Errorf("failed to unpack %q from %q: %w",
				entry.Name, src, err)
		}

		content.files = append(content.files, entry.Name)
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
	if err := checkFragments(previous, next); err != nil {
		return err //nolint:wrapcheck
	}

	return checkJournalNames(previous, next) //nolint:wrapcheck
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

	if end := vclockSignature(next.fragment.VclockEnd); end < previousEnd {
		return fmt.Errorf("%w: archive %q ends at %d, before %q ends at %d: give the full "+
			"backup first, then every increment in the order it was taken",
			ErrValidation, next.path, end, previous.path, previousEnd)
	}

	if begin := vclockSignature(next.fragment.VclockBegin); begin > previousEnd {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	if _, err := io.Copy(out, body); err != nil {
		_ = out.Close()

		return fmt.Errorf("failed to write content: %w", err)
	}

	if err := out.Close(); err != nil {
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
	if err := xlog.PatchInstanceUUID(path, tmp, newUUID); err != nil {
		return false, fmt.Errorf("failed to patch instance uuid in %q: %w", path, err)
	}

	if err := os.Rename(tmp, path); err != nil {
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
func trimAtPoint(workDir string, point Point) (string, []string, error) {
	src, err := xlog.FindTrimFile(workDir, point.ReplicaID, int64(point.LSN))
	if err != nil {
		if errors.Is(err, xlog.ErrTrimFileNotFound) {
			return "", nil, fmt.Errorf("%w: %s in %q", ErrNoTrimFile, point, workDir)
		}

		return "", nil, fmt.Errorf("failed to locate the xlog to trim: %w", err)
	}

	// A backup covers a range, and the point sits somewhere inside it, so the
	// chain usually continues past the file being cut. Those files have to go:
	// Tarantool replays every journal it finds, so leaving them would carry
	// the instance straight through the point it was restored to — and the
	// result looks healthy, it is simply the wrong state.
	dropped, err := dropJournalsAfter(workDir, src)
	if err != nil {
		return "", nil, err //nolint:wrapcheck
	}

	tmp := src + trimTmpSuffix
	if err := xlog.TruncateAt(src, tmp, point.ReplicaID, int64(point.LSN)); err != nil {
		return "", nil, fmt.Errorf("failed to trim %q at %s: %w", src, point, err)
	}

	if err := os.Rename(tmp, src); err != nil {
		return "", nil, fmt.Errorf("failed to replace %q with its trimmed copy: %w", src, err)
	}

	return filepath.Base(src), dropped, nil
}

// dropJournalsAfter removes the snapshots and xlogs that start past the file
// holding the point, and returns their base names. It runs before the trim so
// the directory is only ever indexed while every file still matches its name.
func dropJournalsAfter(workDir, trimFile string) ([]string, error) {
	signature, err := xlog.SignatureOf(trimFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read the position of %q: %w", trimFile, err)
	}

	paths, err := xlog.JournalsAfter(workDir, signature)
	if err != nil {
		return nil, fmt.Errorf("failed to list the files past the recovery point: %w", err)
	}

	dropped := make([]string, 0, len(paths))

	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("failed to remove %q: %w", path, err)
		}

		dropped = append(dropped, filepath.Base(path))
	}

	return dropped, nil
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
		if _, ok := dropped[name]; !ok {
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
