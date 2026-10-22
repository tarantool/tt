package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/sdk/backup/archive"
	"github.com/tarantool/tt/v3/cli/backup/xlog"
	"github.com/tarantool/tt/v3/cli/connector"
)

var (
	errStartANewCheckpointIsTakenForAFullBackupOn = errors.New(
		"a new checkpoint is taken for a full backup only: an incremental backup " +
			"ships no checkpoint",
	)
	errStartBackupFile = errors.New("backup file")
)

const (
	backupDirectoryMode = 0o755
	backupArtifactMode  = 0o644
	// zstdCompressionLevel is the compression level used for the local archive.
	zstdCompressionLevel = 3
	localBackupRootDir   = "tt-backup"
)

// ErrAlreadyInProgress signals that box.backup is already open on the instance.
var ErrAlreadyInProgress = errors.New(
	"backup already in progress, run finalize first",
)

// errNoBackupAfterStart signals that box.backup.info() unexpectedly returned no
// backup right after a successful box.backup.start().
var errNoBackupAfterStart = errors.New(
	"box.backup.info returned no backup after start",
)

// BackupStartOpts are the parameters of tt backup start.
type BackupStartOpts struct {
	// BackupID identifies the backup; used in the archive path.
	BackupID string
	// FromVclock selects an incremental backup; nil means a full backup.
	FromVclock Vclock
	// TTL is the backup lease duration. Zero falls back to the default (1h).
	TTL time.Duration
	// InstName is a fallback instance name (from <APP:INSTANCE>).
	InstName string
	// Dir is the directory the archive and the manifest fragment are written
	// to. Empty means <os.TempDir()>/tt-backup/<backup-id>.
	Dir string
	// Snapshot takes a new checkpoint with box.snapshot() before a full backup
	// opens. Without it the full ships the newest checkpoint as it is. Full
	// backups only.
	Snapshot bool
}

// Start opens box.backup on the instance, packs the WAL files and a per-shard
// fragment into a .tar.zst archive in opts.Dir (or under
// <os.TempDir()>/tt-backup/<backup-id>/ when it is empty), and leaves
// box.backup open. The archive path is returned; the caller is expected to
// print it to stdout. A run that produces no archive closes box.backup again.
func Start(conn connector.Connector, opts BackupStartOpts) (string, error) {
	// The id names both the archive directory and the file base name below it.
	// Checking it before box.backup is opened keeps a malformed run from
	// leaving a lease behind, and keeps the archive inside the backup root.
	validateBackupIDErr := ValidateBackupID(opts.BackupID)
	if validateBackupIDErr != nil {
		return "", validateBackupIDErr
	}

	if opts.Snapshot && opts.FromVclock != nil {
		return "", errStartANewCheckpointIsTakenForAFullBackupOn
	}

	info, err := openBackup(conn, opts)
	if err != nil {
		return "", fmt.Errorf("failed to open backup: %w", err)
	}

	archivePath, err := buildArchive(conn, info, opts)
	if err != nil {
		// An open backup pins the instance's WAL and checkpoint gc until the
		// TTL expires and blocks every later start, so a run that cannot
		// deliver an archive must close it again.
		stopErr := stopBackup(conn)
		if stopErr != nil {
			return "", fmt.Errorf("%w; the backup was left open: %w", err, stopErr)
		}

		return "", err
	}

	return archivePath, nil
}

// resolveArchiveDir returns the directory that holds the archive and the
// manifest fragment of backupID: dir itself when the caller named one, and
// <os.TempDir()>/tt-backup/<backup-id> otherwise. A named dir is made absolute,
// so the archive path printed to stdout addresses the same file from any
// working directory.
func resolveArchiveDir(dir, backupID string) (string, error) {
	if dir == "" {
		return filepath.Join(os.TempDir(), localBackupRootDir, backupID), nil
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve backup directory %q: %w", dir, err)
	}

	return absDir, nil
}

// buildArchive packs the open backup into a local archive. Every error it
// returns leaves box.backup open for the caller to roll back.
func buildArchive(
	conn connector.Connector,
	info *BackupInfo,
	opts BackupStartOpts,
) (string, error) {
	inst, err := resolveInstance(conn, opts)
	if err != nil {
		return "", fmt.Errorf("failed to resolve instance: %w", err)
	}

	archiveDir, err := resolveArchiveDir(opts.Dir, opts.BackupID)
	if err != nil {
		return "", err
	}

	mkdirAllErr := os.MkdirAll(archiveDir, backupDirectoryMode)
	if mkdirAllErr != nil {
		return "",
			fmt.Errorf("failed to create archive directory %q: %w",
				archiveDir,
				mkdirAllErr)
	}

	// The archive and the manifest fragment share the same base name
	// (<backup-id>-<replicaset_uuid>); the archive has .tar.zst, the fragment .json.
	baseName := opts.BackupID + "-" + inst.ReplicasetUUID

	filePaths, err := resolveFiles(
		info.Files,
		inst.WalDir,
		inst.MemtxDir,
		inst.VinylDir,
	)
	if err != nil {
		return "", fmt.Errorf("failed to resolve backup files: %w", err)
	}

	members, instanceID, err := fragmentMembership(conn, inst, filePaths, info.Vclock)
	if err != nil {
		return "", fmt.Errorf("failed to resolve the replicaset membership: %w", err)
	}

	archivePath, err := packArchive(
		archiveDir, baseName, filePaths, members, instanceID, info, inst,
	)
	if err != nil {
		return "", fmt.Errorf("failed to pack archive: %w", err)
	}

	return archivePath, nil
}

// openBackup fails loud if box.backup is already open (ErrAlreadyInProgress,
// no silent cleanup, whether the pre-check or the instance itself catches it),
// otherwise opens it and returns box.backup.info().
func openBackup(conn connector.Connector, opts BackupStartOpts) (*BackupInfo, error) {
	info, err := GetInfo(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to check backup state: %w", err)
	}

	if info != nil {
		return nil, fmt.Errorf("%w (type=%s, vclock_begin=%v)",
			ErrAlreadyInProgress, info.Type, info.PrevVclock)
	}

	// A full backup ships the newest checkpoint; --snapshot makes it a fresh
	// one. Start refuses the flag for an increment, which ships journals only.
	if opts.Snapshot {
		err := snapshotCheckpoint(conn)
		if err != nil {
			return nil, err
		}
	}

	startBackupErr := startBackup(conn, StartOpts{
		FromVclock: opts.FromVclock,
		TTL:        opts.TTL,
	})
	if startBackupErr != nil {
		// The check above cannot catch a run that lost the race after it: both
		// starts read an empty box.backup.info() and only the instance rejects
		// the second one. That loser is exactly as stuck as one the check
		// caught, and the caller routes to its stuck-backup branch on the
		// sentinel alone, so the instance-side refusal has to carry it too.
		if isAlreadyInProgress(startBackupErr) {
			return nil, fmt.Errorf("%w: %w", ErrAlreadyInProgress, startBackupErr)
		}

		return nil, fmt.Errorf("failed to open backup: %w", startBackupErr)
	}

	info, err = GetInfo(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read backup info: %w", err)
	}

	if info == nil {
		// Should not happen right after a successful start, but guard anyway.
		return nil, errNoBackupAfterStart
	}

	return info, nil
}

// isAlreadyInProgress reports whether the instance refused box.backup.start()
// because a backup is already open there. ER_BACKUP_IN_PROGRESS is the stable
// signal and is checked both in the response header and in the box.error
// object, which keeps the original code when Lua re-raises the failure. The
// message is the fallback for connections that deliver the instance's error
// as plain text, with no code to inspect.
func isAlreadyInProgress(err error) bool {
	const (
		code = iproto.ER_BACKUP_IN_PROGRESS
		// Lowercased, so the match survives a change of case upstream.
		msg = "backup is already in progress"
	)

	if tntErr, ok := errors.AsType[tarantool.ServerError](err); ok {
		if tntErr.Code == code {
			return true
		}

		for boxErr := tntErr.ExtendedInfo; boxErr != nil; boxErr = boxErr.Prev {
			if boxErr.Code == uint64(code) {
				return true
			}
		}
	}

	return strings.Contains(strings.ToLower(err.Error()), msg)
}

// resolveInstance fetches instance metadata (UUIDs, name, and data dirs) and
// applies the <APP:INSTANCE> fallback name when box.info.name is empty
// (Tarantool 2.x).
func resolveInstance(conn connector.Connector, opts BackupStartOpts) (*InstanceInfo, error) {
	inst, err := GetInstanceInfo(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve instance metadata: %w", err)
	}

	if inst.InstanceName == "" && opts.InstName != "" {
		inst.InstanceName = opts.InstName
	}

	return inst, nil
}

// packArchive packs filePaths into <archiveDir>/<baseName>.tar.zst, computes
// its checksum, and writes the manifest fragment to
// <archiveDir>/<baseName>.json. On any failure after the archive has been
// packed, both the archive and the fragment are removed — no dangling
// artefact is left behind for finalize/gc to trip over.
func packArchive(
	archiveDir, baseName string,
	filePaths []string,
	members []ClusterMember,
	instanceID uint32,
	info *BackupInfo,
	inst *InstanceInfo,
) (string, error) {
	archivePath := filepath.Join(archiveDir, baseName+".tar.zst")
	fragmentPath := filepath.Join(archiveDir, baseName+".json")

	dataDirs := archive.DataDirs{
		WAL:   inst.WalDir,
		Memtx: inst.MemtxDir,
		Vinyl: inst.VinylDir,
	}

	packErr := archive.Pack(archivePath, filePaths, zstdCompressionLevel, dataDirs)
	if packErr != nil {
		return "", fmt.Errorf("failed to pack archive %q: %w", archivePath, packErr)
	}

	cleanup := func() {
		_ = os.Remove(archivePath)
		_ = os.Remove(fragmentPath)
	}

	checksum, err := archive.Checksum(archivePath)
	if err != nil {
		cleanup()

		return "", fmt.Errorf("failed to checksum archive %q: %w", archivePath, err)
	}

	// Map Tarantool 3.8.0 fields to fragment vclocks:
	//   vclock      -> VclockEnd (always present)
	//   prev_vclock -> VclockBegin (incremental only)
	// For full backups VclockBegin is nil (checkpoint_vclock is cleared
	// from the output by Tarantool).
	fragment := Fragment{
		ReplicasetUUID: inst.ReplicasetUUID,
		InstanceUUID:   inst.InstanceUUID,
		InstanceName:   inst.InstanceName,
		InstanceID:     instanceID,
		Hostname:       inst.Hostname,
		Members:        members,
		Type:           info.Type,
		VclockBegin:    info.PrevVclock,
		VclockEnd:      info.Vclock,
		Files:          archiveEntryNames(filePaths, dataDirs),
		RecoveryPoints: info.RecoveryPoints,
		ChecksumSHA256: checksum,
	}

	writeFragmentErr := writeFragment(fragmentPath, &fragment)
	if writeFragmentErr != nil {
		cleanup()

		return "", fmt.Errorf("failed to write fragment %q: %w", fragmentPath, writeFragmentErr)
	}

	return archivePath, nil
}

// resolveFiles maps backup file names to full paths. Tarantool 3.8.0+
// returns absolute paths in box.backup.info().files. But in the first
// version, file names were returned as base names only, so we handle both.
// If the name is already an existing file (absolute or relative), use it as-is;
// otherwise look it up in all Tarantool data directories
// (wal_dir, memtx_dir, and vinyl_dir).
func resolveFiles(files []string, dataDirs ...string) ([]string, error) {
	paths := make([]string, 0, len(files))
	for _, name := range files {
		// Absolute path or existing relative file — use as-is.
		if filepath.IsAbs(name) {
			_, statErr := os.Stat(name)
			if statErr == nil {
				paths = append(paths, name)
				continue
			}
		}

		found := ""

		for _, dir := range dataDirs {
			if dir == "" {
				continue
			}

			candidate := filepath.Join(dir, name)

			_, statErr2 := os.Stat(candidate)
			if statErr2 == nil {
				found = candidate
				break
			}
		}

		if found == "" {
			return nil, fmt.Errorf(
				"%w %q not found in data directories %q", errStartBackupFile,
				name, dataDirs,
			)
		}

		paths = append(paths, found)
	}

	return paths, nil
}

// archiveEntryNames returns the name each path would get inside the archive.
func archiveEntryNames(paths []string, dataDirs archive.DataDirs) []string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = archive.EntryName(p, dataDirs)
	}

	return names
}

// fragmentMembership derives the membership a fragment records from the
// backup's own data. A full backup lists the .snap of the checkpoint the
// freeze pinned and the journals of the frozen range
// [checkpoint .. vclock_end]: the membership is that snapshot folded with
// those journals, clamped at the frozen vclock (xlog.MembershipUpTo). A
// member whose registration row the frozen range carries is one the backup's
// data restores, whichever side of the checkpoint it registered on -- a
// snapshot-only read would miss it, and the plan would send its node to
// rejoin over data that boots it whole. The clamp also keeps out what a
// still-open journal gains after the freeze: the files keep growing until
// the backup is finalized, and rows past vclock_end are outside the promise
// the fragment makes.
//
// It returns the members but the backed-up instance itself (see
// fragmentMembers) and the backed-up instance's own _cluster id: its row is
// among the ones read, and the manifest names it on the master's topology
// entry like it names every member's.
//
// An incremental backup lists no .snap of its own, and the full's checkpoint
// is not at hand at pack time (only the files this backup lists are), so
// there the membership falls back to a live read. That membership is what
// the instance holds when the fragment is packed, not what the chain's full
// backup froze, so it may drift from the freeze; the warning says so -- the
// warning does not make the read trustworthy. A snapshot that is present but
// unreadable is not a fallback case -- the backup's own checkpoint is
// broken, and that is reported, not papered over with a live state the
// frozen range does not cover.
func fragmentMembership(
	conn connector.Connector,
	inst *InstanceInfo,
	filePaths []string,
	frozen Vclock,
) ([]ClusterMember, uint32, error) {
	var rows []ClusterMember

	snap := snapshotOf(filePaths)
	if snap == "" {
		log.Warnf("no snapshot among the backup files: recording the live " +
			"_cluster membership; it is a read taken after the freeze and " +
			"may drift from the range the backup froze")

		live, err := GetClusterMembers(conn)
		if err != nil {
			return nil, 0, err
		}

		rows = live
	} else {
		bound, err := frozenVClock(frozen)
		if err != nil {
			return nil, 0, err
		}

		folded, err := xlog.MembershipUpTo(snap, journalsOf(filePaths), bound)
		if err != nil {
			return nil, 0, err
		}

		rows = make([]ClusterMember, len(folded))
		for i, member := range folded {
			// Same fields, different package: the fold speaks its own member
			// type, the fragment and the manifest speak this one.
			rows[i] = ClusterMember(member)
		}
	}

	// The instance's own row is told by uuid, not by id -- the id only says
	// when the node joined, not which node took the backup.
	var instanceID uint32

	for _, member := range rows {
		if member.UUID == inst.InstanceUUID {
			instanceID = member.ID
			break
		}
	}

	return fragmentMembers(inst, rows), instanceID, nil
}

// journalsOf returns the .xlog files of the backed-up set in replay order:
// the order of their names, which is the order of the vclock signatures the
// names encode (the same order the restore side folds the chain in).
func journalsOf(filePaths []string) []string {
	journals := make([]string, 0, len(filePaths))
	for _, path := range filePaths {
		if strings.HasSuffix(path, ".xlog") {
			journals = append(journals, path)
		}
	}

	slices.SortFunc(journals, func(a, b string) int {
		return strings.Compare(filepath.Base(a), filepath.Base(b))
	})

	return journals
}

// frozenVClock converts the frozen vclock box.backup.start returned into the
// fold's bound. An LSN past int64 is refused rather than wrapped: a negative
// component would clamp a whole replica axis out of the fold.
func frozenVClock(v Vclock) (format.VClock, error) {
	out := make(format.VClock, len(v))
	for replicaID, lsn := range v {
		if lsn > math.MaxInt64 {
			return nil, fmt.Errorf("%w: replica %d lsn %d exceeds int64",
				xlog.ErrLSNOverflow, replicaID, lsn)
		}

		out[replicaID] = int64(lsn)
	}

	return out, nil
}

// fragmentMembers lists the replicaset members the fragment records: every
// member of the given membership but the backed-up instance itself, ordered
// by name. The instance's own row is told by uuid, not by id -- the id only
// says when the node joined, not which node took the backup. nil means the
// replicaset has no other members and the fragment omits the field.
func fragmentMembers(inst *InstanceInfo, rows []ClusterMember) []ClusterMember {
	members := make([]ClusterMember, 0, len(rows))
	for _, member := range rows {
		if member.UUID == inst.InstanceUUID {
			continue
		}

		members = append(members, member)
	}

	if len(members) == 0 {
		return nil
	}

	slices.SortStableFunc(members, compareClusterMembers)

	return members
}

// snapshotOf returns the path of the .snap among the backed-up files: the
// checkpoint the freeze produced, whose _cluster rows seed the membership
// the backup restores. The newest snapshot by the vclock signature its name
// carries wins, so a future box.backup listing more than one decides the
// membership by recency, not by list order. An incremental lists none, and
// "" comes back for it.
func snapshotOf(filePaths []string) string {
	newest := ""

	var newestSignature int64

	for _, path := range filePaths {
		if !strings.HasSuffix(path, ".snap") {
			continue
		}

		signature, err := strconv.ParseInt(
			strings.TrimSuffix(filepath.Base(path), ".snap"), 10, 64,
		)
		if err != nil {
			continue
		}

		if newest == "" || signature > newestSignature {
			newest, newestSignature = path, signature
		}
	}

	return newest
}

// writeFragment marshals the fragment to a JSON file at path.
func writeFragment(path string, fragment *Fragment) error {
	data, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal fragment: %w", err)
	}

	writeFileErr := os.WriteFile(path, data, backupArtifactMode)
	if writeFileErr != nil {
		return fmt.Errorf("failed to write fragment %q: %w", path, writeFileErr)
	}

	return nil
}
