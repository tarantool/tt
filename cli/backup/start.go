package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool"

	"github.com/tarantool/tt/cli/backup/archive"
	"github.com/tarantool/tt/cli/connector"
)

const (
	// zstdCompressionLevel is the compression level used for the local archive.
	zstdCompressionLevel = 3
	localBackupRootDir   = "tt-backup"
)

// ErrAlreadyInProgress signals that box.backup is already open on the instance.
var ErrAlreadyInProgress = errors.New(
	"backup already in progress, run finalize first")

// errNoBackupAfterStart signals that box.backup.info() unexpectedly returned no
// backup right after a successful box.backup.start().
var errNoBackupAfterStart = errors.New("box.backup.info returned no backup after start")

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
}

// Start opens box.backup on the instance, packs the WAL files and a
// per-shard fragment into a .tar.zst archive under
// /tmp/tt-backup/<backup-id>/, and leaves box.backup open. The archive path is
// returned; the caller is expected to print it to stdout. A run that produces
// no archive closes box.backup again.
func Start(conn connector.Connector, opts BackupStartOpts) (string, error) {
	// The id names both the archive directory and the file base name below it.
	// Checking it before box.backup is opened keeps a malformed run from
	// leaving a lease behind, and keeps the archive inside the backup root.
	if err := ValidateBackupID(opts.BackupID); err != nil {
		return "", err //nolint:wrapcheck
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
		if stopErr := stopBackup(conn); stopErr != nil {
			return "", fmt.Errorf("%w; the backup was left open: %w", err, stopErr)
		}
		return "", err //nolint:wrapcheck
	}

	return archivePath, nil
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

	archiveDir := filepath.Join(os.TempDir(), localBackupRootDir, opts.BackupID)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create archive directory %q: %w", archiveDir, err)
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

	archivePath, err := packArchive(archiveDir, baseName, filePaths, info, inst)
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

	if err := startBackup(conn, StartOpts{
		FromVclock: opts.FromVclock,
		TTL:        opts.TTL,
	}); err != nil {
		// The check above cannot catch a run that lost the race after it: both
		// starts read an empty box.backup.info() and only the instance rejects
		// the second one. That loser is exactly as stuck as one the check
		// caught, and the caller routes to its stuck-backup branch on the
		// sentinel alone, so the instance-side refusal has to carry it too.
		if isAlreadyInProgress(err) {
			return nil, fmt.Errorf("%w: %w", ErrAlreadyInProgress, err)
		}
		return nil, fmt.Errorf("failed to open backup: %w", err)
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
		code = uint32(iproto.ER_BACKUP_IN_PROGRESS)
		// Lowercased, so the match survives a change of case upstream.
		msg = "backup is already in progress"
	)

	var tntErr tarantool.Error
	if errors.As(err, &tntErr) {
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
	info *BackupInfo,
	inst *InstanceInfo,
) (string, error) {
	archivePath := filepath.Join(archiveDir, baseName+".tar.zst")
	fragmentPath := filepath.Join(archiveDir, baseName+".json")

	dataDirs := []string{inst.WalDir, inst.MemtxDir, inst.VinylDir}

	if err := archive.Pack(archivePath, filePaths, zstdCompressionLevel, dataDirs...); err != nil {
		return "", fmt.Errorf("failed to pack archive %q: %w", archivePath, err)
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
		Hostname:       inst.Hostname,
		Type:           info.Type,
		VclockBegin:    info.PrevVclock,
		VclockEnd:      info.Vclock,
		Files:          archiveEntryNames(filePaths, dataDirs...),
		RecoveryPoints: info.RecoveryPoints,
		ChecksumSHA256: checksum,
	}

	if err := writeFragment(fragmentPath, &fragment); err != nil {
		cleanup()
		return "", fmt.Errorf("failed to write fragment %q: %w", fragmentPath, err)
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
			if _, err := os.Stat(name); err == nil {
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
			if _, err := os.Stat(candidate); err == nil {
				found = candidate
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf(
				"backup file %q not found in data directories %q",
				name, dataDirs)
		}
		paths = append(paths, found)
	}
	return paths, nil
}

// archiveEntryNames returns the name each path would get inside the archive.
func archiveEntryNames(paths []string, dataDirs ...string) []string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = archive.EntryName(p, dataDirs...)
	}

	return names
}

// writeFragment marshals the fragment to a JSON file at path.
func writeFragment(path string, fragment *Fragment) error {
	data, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal fragment: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write fragment %q: %w", path, err)
	}
	return nil
}
