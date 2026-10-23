package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tarantool/tt/sdk/backup/storage"
)

// tempFilePrefix is the reserved name prefix for temporary files created during Put.
const tempFilePrefix = ".tt-backup-"

const (
	directoryMode = 0o755
	objectMode    = 0o644
)

// tempFilePattern is the glob pattern passed to os.CreateTemp for temporary files.
const tempFilePattern = tempFilePrefix + "*"

// staleTempFileAge is how old a leftover temp file must be before New sweeps it.
// The threshold keeps the sweep from racing a concurrent Put in another process.
const staleTempFileAge = 24 * time.Hour

var (
	errPathRequired = errors.New("fs storage path is required")
	errNegativeSize = errors.New("fs object size must be non-negative")
	errRootNotFound = fmt.Errorf("%w: no such storage root", storage.ErrStorageMissing)
	errNotDirectory = errors.New("storage path is not a directory")
)

// Config describes local filesystem storage configuration.
type Config struct {
	Path   string
	Prefix string
}

// Storage is a local filesystem backup storage backend.
type Storage struct {
	// base is the configured path, prefix excluded. Listing requires it to exist
	// as a directory: an operator points at it explicitly, so a missing one is a
	// misconfigured storage rather than an empty one.
	base string
	// root is base joined with the prefix. It, and everything below it, is created
	// on demand by Put, so its absence only means nothing has been stored yet.
	root string
	// sweepOnce keeps the stale-temp-file sweep on the write path: a process that
	// never puts anything must not delete anything either.
	sweepOnce sync.Once
}

// New opens local filesystem backup storage.
// The root directory is created lazily on the first Put call; List, by contrast,
// requires the configured path to exist, so a misconfigured storage cannot read
// as an empty one.
func New(cfg Config) (*Storage, error) {
	base := strings.TrimSpace(cfg.Path)
	if base == "" {
		return nil, errPathRequired
	}

	// Resolve the base to an absolute path once so path-escape checks are
	// independent of the process working directory at call time.
	base, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve storage root %q: %w", cfg.Path, err)
	}

	root := base

	if cfg.Prefix != "" {
		prefix, err := storage.CleanPrefix(cfg.Prefix)
		if err != nil {
			return nil, fmt.Errorf("failed to clean storage prefix %q: %w", cfg.Prefix, err)
		}

		if prefix != "" {
			root = filepath.Join(base, filepath.FromSlash(strings.TrimRight(prefix, "/")))
		}
	}

	return &Storage{base: base, root: root, sweepOnce: sync.Once{}}, nil
}

// List returns objects whose keys start with the given prefix, sorted by key.
// A storage whose configured path is missing, or is shadowed by something that
// is not a directory, is an error: reported as an empty listing it would make a
// typo in the storage URI read as "nothing was ever backed up here", which is
// what tt backup plan answers with "start a new chain".
func (s *Storage) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	contextErr := ctx.Err()
	if contextErr != nil {
		return nil, fmt.Errorf("failed to list prefix %q: %w", prefix, contextErr)
	}

	cleanPrefix, err := storage.CleanPrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to clean list prefix %q: %w", prefix, err)
	}

	baseErr := s.checkBase()
	if baseErr != nil {
		return nil, fmt.Errorf("failed to list prefix %q: %w", cleanPrefix, baseErr)
	}

	root := filepath.Join(s.root, filepath.FromSlash(path.Dir(cleanPrefix)))

	objects := make([]storage.ObjectInfo, 0)
	// Below the base everything is created on demand, so an absent prefix
	// directory is a genuinely empty listing: that is how a storage looks before
	// its first upload, and mid-upload with data/ written and no manifest yet.
	switch exists, err := statDir(root); {
	case err != nil:
		return nil, fmt.Errorf("failed to stat prefix %q: %w", cleanPrefix, err)
	case !exists:
		return objects, nil
	}

	walkErr := s.walkDir(ctx, root, cleanPrefix, &objects)
	if walkErr != nil {
		return nil, fmt.Errorf("failed to list prefix %q: %w", cleanPrefix, walkErr)
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	return objects, nil
}

// statDir reports whether path exists as a directory. A path occupied by
// something else is an error rather than a missing directory, so a file
// shadowing a storage directory cannot pass for an empty one.
func statDir(path string) (bool, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to stat %q: %w", path, err)
	case !info.IsDir():
		return false, fmt.Errorf("%w: %q", errNotDirectory, path)
	}

	return true, nil
}

// Get opens the object for reading, returning storage.ErrKeyNotFound if it is absent.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	contextErr := ctx.Err()
	if contextErr != nil {
		return nil, fmt.Errorf("failed to get object %q: %w", key, contextErr)
	}

	path, err := s.objectPath(key)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve object path %q: %w", key, err)
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, storage.ErrKeyNotFound
		}

		return nil, fmt.Errorf("failed to open object %q: %w", key, err)
	}

	// A key that resolves to a directory is not a stored object; report it as
	// missing here rather than deferring an opaque EISDIR to the caller's Read.
	info, err := file.Stat()
	switch {
	case err != nil:
		_ = file.Close()
		return nil, fmt.Errorf("failed to stat object %q: %w", key, err)
	case !info.Mode().IsRegular():
		_ = file.Close()
		return nil, storage.ErrKeyNotFound
	}

	return file, nil
}

// Put stores an object to the filesystem via a temp file and atomic rename.
// size must be the exact, non-negative number of bytes r yields; a mismatch is
// an error so a wrong size cannot silently store a truncated object.
func (s *Storage) Put(ctx context.Context, key string, reader io.Reader, size int64) error {
	contextErr := ctx.Err()
	if contextErr != nil {
		return fmt.Errorf("failed to put object %q: %w", key, contextErr)
	}

	s.sweepOnce.Do(s.sweepStaleTempFiles)

	path, err := s.objectPath(key)
	if err != nil {
		return fmt.Errorf("failed to resolve object path %q: %w", key, err)
	}

	if size < 0 {
		return fmt.Errorf("failed to put object %q: %w", key, errNegativeSize)
	}

	dir := filepath.Dir(path)

	mkdirErr := os.MkdirAll(dir, directoryMode)
	if mkdirErr != nil {
		return fmt.Errorf("failed to create object directory for %q: %w", key, mkdirErr)
	}

	tmp, err := os.CreateTemp(dir, tempFilePattern)
	if err != nil {
		return fmt.Errorf("failed to create temporary object %q: %w", key, err)
	}

	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	written, err := io.Copy(tmp, ctxReader{ctx, reader})
	switch {
	case err != nil:
		_ = tmp.Close()
		return fmt.Errorf("failed to write object %q: %w", key, err)
	case written != size:
		_ = tmp.Close()

		//nolint:err113 // Preserve the source error message without introducing a sentinel.
		return fmt.Errorf("failed to write object %q: wrote %d bytes, expected %d",
			key, written, size)
	}

	// os.CreateTemp makes the file 0600; widen it to match the 0755 directories
	// so a backup written by one user can be read back by another.
	chmodErr := tmp.Chmod(objectMode)
	if chmodErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set object mode %q: %w", key, chmodErr)
	}

	// Flush the data before the rename so a crash cannot leave the object present
	// under its final name but truncated or empty.
	syncErr := tmp.Sync()
	if syncErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to sync object %q: %w", key, syncErr)
	}

	closeErr := tmp.Close()
	if closeErr != nil {
		return fmt.Errorf("failed to close object %q: %w", key, closeErr)
	}

	renameErr := os.Rename(tmpPath, path)
	if renameErr != nil {
		return fmt.Errorf("failed to store object %q: %w", key, renameErr)
	}

	// Flush the directory so the rename itself survives a crash.
	syncDirErr := syncDir(dir)
	if syncDirErr != nil {
		return fmt.Errorf("failed to persist object %q: %w", key, syncDirErr)
	}

	return nil
}

// syncDir flushes a directory to disk so a rename within it is durable across a crash.
func syncDir(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open directory %q: %w", dir, err)
	}

	syncErr := directory.Sync()
	if syncErr != nil {
		_ = directory.Close()
		return fmt.Errorf("failed to sync directory %q: %w", dir, syncErr)
	}

	closeErr := directory.Close()
	if closeErr != nil {
		return fmt.Errorf("failed to close directory %q: %w", dir, closeErr)
	}

	return nil
}

// Delete removes the object; a missing object is not an error.
func (s *Storage) Delete(ctx context.Context, key string) error {
	contextErr := ctx.Err()
	if contextErr != nil {
		return fmt.Errorf("failed to delete object %q: %w", key, contextErr)
	}

	path, err := s.objectPath(key)
	if err != nil {
		return fmt.Errorf("failed to resolve object path %q: %w", key, err)
	}

	removeErr := os.Remove(path)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("failed to delete object %q: %w", key, removeErr)
	}

	return nil
}

// sweepStaleTempFiles best-effort removes temp files left behind by interrupted
// Put calls (process killed between os.CreateTemp and the deferred os.Remove).
// Such files are hidden from List and cannot be Deleted through the API, so they
// would otherwise accumulate forever. Errors are ignored: this is an optimization,
// not a guarantee, and the root may not exist yet.
//
// It runs on the first Put rather than in New. Only Put creates these files, and
// read-only callers - tt backup verify above all, whose contract is that it
// deletes nothing whatever it finds - must not remove anything just by opening
// the storage.
func (s *Storage) sweepStaleTempFiles() {
	cutoff := time.Now().Add(-staleTempFileAge)

	_ = filepath.WalkDir(s.root, func(path string, entry os.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil //nolint:nilerr // Stale temporary file cleanup is best effort.
		case entry.IsDir() || !isTempFile(entry.Name()):
			return nil
		}

		info, err := entry.Info()
		switch {
		case err != nil:
			return nil //nolint:nilerr // Stale temporary file cleanup is best effort.
		case info.ModTime().Before(cutoff):
			_ = os.Remove(path) //nolint:gosec // Preserve best-effort stale temporary file cleanup.
		}

		return nil
	})
}

// checkBase fails when the configured storage path is missing or is not a
// directory. Only the base is checked: the prefix under it is created on demand
// by Put, so an absent prefix is an empty storage rather than a broken one.
func (s *Storage) checkBase() error {
	switch exists, err := statDir(s.base); {
	case err != nil:
		return err
	case !exists:
		return fmt.Errorf("%w: %q", errRootNotFound, s.base)
	}

	return nil
}

// walkDir traverses root and appends matching files to objects, honoring ctx cancellation.
func (s *Storage) walkDir(
	ctx context.Context,
	root string,
	prefix string,
	objects *[]storage.ObjectInfo,
) error {
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if entry.IsDir() || isTempFile(entry.Name()) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("failed to get file info for %q: %w", path, err)
		}

		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %q: %w", path, err)
		}

		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}

		*objects = append(*objects, storage.ObjectInfo{
			Key:          key,
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})

		return nil
	})
	if err != nil {
		return fmt.Errorf("walkdir failed: %w", err)
	}

	return nil
}

// objectPath resolves a key to an absolute filesystem path within the storage root,
// rejecting keys that escape it.
func (s *Storage) objectPath(key string) (string, error) {
	cleanKey, err := storage.CleanKey(key)
	if err != nil {
		return "", fmt.Errorf("failed to clean object key %q: %w", key, err)
	}

	// Reject keys whose file name collides with the reserved temp-file prefix,
	// otherwise such objects would be hidden from List.
	if isTempFile(filepath.Base(cleanKey)) {
		return "", fmt.Errorf("storage key %q uses reserved prefix %q: %w",
			key, tempFilePrefix, storage.ErrInvalidKey)
	}

	path := filepath.Join(s.root, filepath.FromSlash(cleanKey))
	// Use a relative-path check rather than a string prefix: the prefix form
	// (s.root + separator) is "//" when the root resolves to "/", which no joined
	// path ever has, so it would reject every key under a root of "/".
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		//nolint:err113 // Preserve the source error message without introducing a sentinel.
		return "", fmt.Errorf("storage key %q escapes storage root", cleanKey)
	}

	return path, nil
}

// ctxReader wraps an io.Reader and checks context cancellation before each Read call.
type ctxReader struct {
	ctx context.Context //nolint:containedctx // The reader retains Put cancellation.
	r   io.Reader
}

func (cr ctxReader) Read(buffer []byte) (int, error) {
	contextErr := cr.ctx.Err()
	if contextErr != nil {
		return 0, fmt.Errorf("context canceled: %w", contextErr)
	}

	count, err := cr.r.Read(buffer)
	if err != nil {
		if err == io.EOF {
			return count, io.EOF
		}

		return count, fmt.Errorf("read failed: %w", err)
	}

	return count, nil
}

// isTempFile reports whether name is a temporary file created during Put.
func isTempFile(name string) bool {
	return strings.HasPrefix(name, tempFilePrefix)
}
