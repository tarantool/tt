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
	"time"

	"github.com/tarantool/tt/lib/backup/storage"
)

// tempFilePrefix is the reserved name prefix for temporary files created during Put.
const tempFilePrefix = ".tt-backup-"

// tempFilePattern is the glob pattern passed to os.CreateTemp for temporary files.
const tempFilePattern = tempFilePrefix + "*"

// staleTempFileAge is how old a leftover temp file must be before New sweeps it.
// The threshold keeps the sweep from racing a concurrent Put in another process.
const staleTempFileAge = 24 * time.Hour

var (
	errPathRequired = errors.New("fs storage path is required")
	errNegativeSize = errors.New("fs object size must be non-negative")
)

// Config describes local filesystem storage configuration.
type Config struct {
	Path   string
	Prefix string
}

// Storage is a local filesystem backup storage backend.
type Storage struct {
	root string
}

// New opens local filesystem backup storage.
// The root directory is created lazily on the first Put call.
func New(cfg Config) (*Storage, error) {
	root := strings.TrimSpace(cfg.Path)
	if root == "" {
		return nil, errPathRequired
	}

	if cfg.Prefix != "" {
		prefix, err := storage.CleanPrefix(cfg.Prefix)
		if err != nil {
			return nil, fmt.Errorf("failed to clean storage prefix %q: %w", cfg.Prefix, err)
		}
		if prefix != "" {
			root = filepath.Join(root, filepath.FromSlash(strings.TrimRight(prefix, "/")))
		}
	}

	// Resolve root to an absolute path once so path-escape checks are independent
	// of the process working directory at call time.
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve storage root %q: %w", cfg.Path, err)
	}

	s := &Storage{root: root}
	s.sweepStaleTempFiles()

	return s, nil
}

// sweepStaleTempFiles best-effort removes temp files left behind by interrupted
// Put calls (process killed between os.CreateTemp and the deferred os.Remove).
// Such files are hidden from List and cannot be Deleted through the API, so they
// would otherwise accumulate forever. Errors are ignored: this is an optimization,
// not a guarantee, and the root may not exist yet.
func (s *Storage) sweepStaleTempFiles() {
	cutoff := time.Now().Add(-staleTempFileAge)
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir() || !isTempFile(d.Name()):
			return nil
		}

		info, err := d.Info()
		switch {
		case err != nil:
			return nil
		case info.ModTime().Before(cutoff):
			_ = os.Remove(path)
		}

		return nil
	})
}

// List returns objects whose keys start with the given prefix, sorted by key.
func (s *Storage) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("failed to list prefix %q: %w", prefix, err)
	}

	cleanPrefix, err := storage.CleanPrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to clean list prefix %q: %w", prefix, err)
	}

	root := filepath.Join(s.root, filepath.FromSlash(path.Dir(cleanPrefix)))

	objects := make([]storage.ObjectInfo, 0)
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return objects, nil
		}

		return nil, fmt.Errorf("failed to stat prefix %q: %w", cleanPrefix, err)
	}

	if err := s.walkDir(ctx, root, cleanPrefix, &objects); err != nil {
		return nil, fmt.Errorf("failed to list prefix %q: %w", cleanPrefix, err)
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	return objects, nil
}

// walkDir traverses root and appends matching files to objects, honoring ctx cancellation.
func (s *Storage) walkDir(
	ctx context.Context,
	root string,
	prefix string,
	objects *[]storage.ObjectInfo,
) error {
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() || isTempFile(d.Name()) {
			return nil
		}

		info, err := d.Info()
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

// Get opens the object for reading, returning storage.ErrKeyNotFound if it is absent.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("failed to get object %q: %w", key, err)
	}

	path, err := s.objectPath(key)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve object path %q: %w", key, err)
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, storage.ErrKeyNotFound
		}
		return nil, fmt.Errorf("failed to open object %q: %w", key, err)
	}

	// A key that resolves to a directory is not a stored object; report it as
	// missing here rather than deferring an opaque EISDIR to the caller's Read.
	info, err := f.Stat()
	switch {
	case err != nil:
		_ = f.Close()
		return nil, fmt.Errorf("failed to stat object %q: %w", key, err)
	case !info.Mode().IsRegular():
		_ = f.Close()
		return nil, storage.ErrKeyNotFound
	}

	return f, nil
}

// Put stores an object to the filesystem via a temp file and atomic rename.
// size must be the exact, non-negative number of bytes r yields; a mismatch is
// an error so a wrong size cannot silently store a truncated object.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("failed to put object %q: %w", key, err)
	}

	path, err := s.objectPath(key)
	if err != nil {
		return fmt.Errorf("failed to resolve object path %q: %w", key, err)
	}

	if size < 0 {
		return fmt.Errorf("failed to put object %q: %w", key, errNegativeSize)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create object directory for %q: %w", key, err)
	}

	tmp, err := os.CreateTemp(dir, tempFilePattern)
	if err != nil {
		return fmt.Errorf("failed to create temporary object %q: %w", key, err)
	}

	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	written, err := io.Copy(tmp, ctxReader{ctx, r})
	switch {
	case err != nil:
		_ = tmp.Close()
		return fmt.Errorf("failed to write object %q: %w", key, err)
	case written != size:
		_ = tmp.Close()
		return fmt.Errorf("failed to write object %q: wrote %d bytes, expected %d",
			key, written, size)
	}

	// os.CreateTemp makes the file 0600; widen it to match the 0755 directories
	// so a backup written by one user can be read back by another.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set object mode %q: %w", key, err)
	}

	// Flush the data before the rename so a crash cannot leave the object present
	// under its final name but truncated or empty.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to sync object %q: %w", key, err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close object %q: %w", key, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to store object %q: %w", key, err)
	}

	// Flush the directory so the rename itself survives a crash.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("failed to persist object %q: %w", key, err)
	}

	return nil
}

// syncDir flushes a directory to disk so a rename within it is durable across a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open directory %q: %w", dir, err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("failed to sync directory %q: %w", dir, err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("failed to close directory %q: %w", dir, err)
	}

	return nil
}

// Delete removes the object; a missing object is not an error.
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("failed to delete object %q: %w", key, err)
	}

	path, err := s.objectPath(key)
	if err != nil {
		return fmt.Errorf("failed to resolve object path %q: %w", key, err)
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to delete object %q: %w", key, err)
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
		return "", fmt.Errorf("storage key %q escapes storage root", cleanKey)
	}

	return path, nil
}

// ctxReader wraps an io.Reader and checks context cancellation before each Read call.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr ctxReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, fmt.Errorf("context canceled: %w", err)
	}

	n, err := cr.r.Read(p)
	if err != nil {
		if err == io.EOF {
			return n, io.EOF
		}

		return n, fmt.Errorf("read failed: %w", err)
	}

	return n, nil
}

// isTempFile reports whether name is a temporary file created during Put.
func isTempFile(name string) bool {
	return strings.HasPrefix(name, tempFilePrefix)
}
