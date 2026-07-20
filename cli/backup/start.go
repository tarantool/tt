package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tarantool/tt/cli/backup/archive"
	"github.com/tarantool/tt/cli/connector"
)

// zstdCompressionLevel is the compression level used for the local archive.
const zstdCompressionLevel = 3

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
// returned; the caller is expected to print it to stdout.
func Start(conn connector.Connector, opts BackupStartOpts) (string, error) {
	info, err := openBackup(conn, opts)
	if err != nil {
		return "", fmt.Errorf("failed to open backup: %w", err)
	}

	inst, err := resolveInstance(conn, opts)
	if err != nil {
		return "", fmt.Errorf("failed to resolve instance: %w", err)
	}

	archiveDir := filepath.Join(os.TempDir(), "tt-backup", opts.BackupID)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create archive directory %q: %w", archiveDir, err)
	}

	// The archive and the manifest fragment share the same base name
	// (<backup-id>-<replicaset_uuid>); the archive has .tar.zst, the fragment .json.
	baseName := fmt.Sprintf("%s-%s", opts.BackupID, inst.ReplicasetUUID)

	filePaths, err := resolveFiles(info.Files, inst.WalDir, inst.MemtxDir)
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
// no silent cleanup), otherwise opens it and returns box.backup.info().
func openBackup(conn connector.Connector, opts BackupStartOpts) (*BackupInfo, error) {
	info, err := GetInfo(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to check backup state: %w", err)
	}
	if info != nil {
		return nil, ErrAlreadyInProgress
	}

	if err := startBackup(conn, StartOpts{
		FromVclock: opts.FromVclock,
		TTL:        opts.TTL,
	}); err != nil {
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

// resolveInstance fetches instance metadata (uuids, name, wal/memtx dirs) and
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

	if err := archive.Pack(archivePath, filePaths, zstdCompressionLevel); err != nil {
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

	fragment := Fragment{
		ReplicasetUUID: inst.ReplicasetUUID,
		InstanceUUID:   inst.InstanceUUID,
		InstanceName:   inst.InstanceName,
		Hostname:       inst.Hostname,
		Type:           info.Type,
		VclockBegin:    info.VclockBegin,
		VclockEnd:      info.VclockEnd,
		Files:          append([]string(nil), info.Files...),
		RecoveryPoints: info.RecoveryPoints,
		ChecksumSHA256: checksum,
	}

	if err := writeFragment(fragmentPath, &fragment); err != nil {
		cleanup()
		return "", fmt.Errorf("failed to write fragment %q: %w", fragmentPath, err)
	}

	return archivePath, nil
}

// resolveFiles maps WAL file base names to full paths by looking them up in
// wal_dir and memtx_dir.
func resolveFiles(files []string, walDir, memtxDir string) ([]string, error) {
	paths := make([]string, 0, len(files))
	for _, name := range files {
		found := ""
		for _, dir := range []string{walDir, memtxDir} {
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
				"backup file %q not found in wal_dir %q or memtx_dir %q",
				name, walDir, memtxDir)
		}
		paths = append(paths, found)
	}
	return paths, nil
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
