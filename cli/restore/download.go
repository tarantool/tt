package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/backup/archive"
	"github.com/tarantool/tt/cli/backup/storage"
)

var (
	// ErrArchiveMissing marks an object a manifest refers to that the storage
	// does not hold. It is kept apart from a transport failure because no retry
	// brings it back: the chain is incomplete, and the operator has to pick
	// another recovery point rather than run the command again.
	ErrArchiveMissing = errors.New("restore: object is not in the backup storage")

	// ErrChecksumMismatch marks a downloaded object whose bytes do not match the
	// checksum its manifest recorded.
	ErrChecksumMismatch = errors.New("restore: checksum does not match the manifest")

	// ErrDestinationTaken marks two objects of the plan that would be downloaded
	// to the same local file.
	ErrDestinationTaken = errors.New(
		"restore: two objects would be downloaded to the same file")
)

// downloadSuffix names a download in progress. A file only takes its final name
// once its bytes are known to be the right ones, so an interrupted run leaves
// nothing that a later run - or an operator reading the directory - would take
// for a complete archive.
const downloadSuffix = ".part"

// downloadDirPerm is the mode of a download directory the command creates.
const downloadDirPerm = 0o755

// fetch copies the object under key into dst and returns its sha256 in hex.
//
// The digest is computed from the same stream that lands on disk, so the sum
// reported is the sum of the bytes that were actually written, not of a second
// read that might see a different file. A non-empty expected checksum that does
// not match leaves nothing behind: a plan is worth nothing if the archives
// behind it are not the ones the manifest describes.
func fetch(
	ctx context.Context,
	store storage.Storage,
	key, dst, expected string,
) (string, error) {
	if reused, err := reuseExisting(dst, expected); err != nil {
		return "", err //nolint:wrapcheck
	} else if reused {
		return expected, nil
	}

	reader, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return "", fmt.Errorf("%w: %q", ErrArchiveMissing, key)
		}

		return "", fmt.Errorf("failed to get object %q: %w", key, err)
	}
	defer reader.Close()

	partial := dst + downloadSuffix
	file, err := os.Create(partial)
	if err != nil {
		return "", fmt.Errorf("failed to create %q: %w", partial, err)
	}

	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, digest), reader)
	closeErr := file.Close()

	switch {
	case copyErr != nil:
		os.Remove(partial)
		return "", fmt.Errorf("failed to download %q: %w", key, copyErr)
	case closeErr != nil:
		os.Remove(partial)
		return "", fmt.Errorf("failed to write %q: %w", partial, closeErr)
	}

	checksum := hex.EncodeToString(digest.Sum(nil))
	if expected != "" && !strings.EqualFold(expected, checksum) {
		os.Remove(partial)
		return "", fmt.Errorf("%w: %q is %s, the manifest says %s",
			ErrChecksumMismatch, key, checksum, expected)
	}

	if err := os.Rename(partial, dst); err != nil {
		os.Remove(partial)
		return "", fmt.Errorf("failed to rename %q to %q: %w", partial, dst, err)
	}

	return checksum, nil
}

// reuseExisting reports whether dst already holds the object, so that a re-run
// of a plan that was interrupted halfway does not pull every archive again -
// restores are re-planned often and the archives are large. Only a checksum can
// establish that, so without one nothing is reused: a same-named file left by
// another run says nothing about its contents.
func reuseExisting(dst, expected string) (bool, error) {
	if expected == "" {
		return false, nil
	}

	switch _, err := os.Stat(dst); {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to stat %q: %w", dst, err)
	}

	checksum, err := archive.Checksum(dst)
	if err != nil {
		return false, fmt.Errorf("failed to check the local copy: %w", err)
	}

	return strings.EqualFold(expected, checksum), nil
}

// destinations records which storage key each local file was claimed by, so
// that no two objects can land on one.
//
// Storage keys are grouped by kind (manifests/, data/), but a restore directory
// is handed to an operator and to scp, so the files land flat under the names
// the keys end with - and two keys ending in the same name would then collapse
// into a single file. Nothing downstream would catch that: each download is
// verified against its own manifest entry and the overwrite happens after the
// first one has already passed, so the plan would hand a replicaset an archive
// taken on another one, with a checksum that belongs to neither file on disk.
type destinations map[string]string

// claim returns where key goes under dir, or an error if a different key is
// already going there. The same key claimed twice is the same object, which is
// what a manifest shared by several replicasets does.
func (d destinations) claim(dir, key string) (string, error) {
	name := filepath.Base(key)
	path := filepath.Join(dir, name)

	switch owner, taken := d[path]; {
	case !taken:
		d[path] = key
	case owner != key:
		return "", fmt.Errorf(
			"%w: objects %q and %q both end in %q", ErrDestinationTaken, owner, key, name)
	}

	return path, nil
}
