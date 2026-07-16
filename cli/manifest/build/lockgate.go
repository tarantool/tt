package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/manifest"
)

// resolver is the slice of resolve.Engine the lock gate drives: report whether
// a lock is stale and (re)resolve a manifest into a fresh lock. *resolve.Engine
// satisfies it; tests substitute a fake so the gate's --locked branching can be
// exercised without a registry.
type resolver interface {
	IsStale(man *manifest.Manifest, lock *manifest.Lock) (bool, string, error)
	Resolve(ctx context.Context, man *manifest.Manifest) (*manifest.Lock, []string, error)
}

// loadLock reads and parses the lock next to the manifest. It never resolves —
// this is the tt package fetch path, which materializes strictly from the lock.
// A missing lock is errNoLock (exit 1): there is nothing to fetch from.
func loadLock(projectDir string) (*manifest.Lock, error) {
	path := filepath.Join(projectDir, lockFileName)

	data, err := os.ReadFile(path) //nolint:gosec // Reads the caller's own lock.
	if errors.Is(err, os.ErrNotExist) {
		return nil, exitErrorf(exitStateError, "%w: %s not found", errNoLock, lockFileName)
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", lockFileName, err)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", lockFileName, err)
	}

	return lock, nil
}

// gateLock resolves the lock per the --locked gate and reports whether the lock
// was rewritten:
//
//   - No lock on disk: --locked fails (errLockStale, exit 1); otherwise resolve
//     fresh and write it.
//   - Lock present and stale: --locked fails with the staleness reason (exit 1);
//     otherwise re-resolve and rewrite.
//   - Lock present and fresh: use it as is, no rewrite.
//
// This is what separates build from fetch: an unflagged build silently updates
// the lock, while fetch (loadLock) never resolves.
func gateLock(
	ctx context.Context, res resolver, man *manifest.Manifest, projectDir string, locked bool,
) (*manifest.Lock, []string, error) {
	path := filepath.Join(projectDir, lockFileName)

	data, readErr := os.ReadFile(path) //nolint:gosec // Reads the caller's own lock.
	if errors.Is(readErr, os.ErrNotExist) {
		if locked {
			return nil, nil, exitErrorf(exitStateError,
				"%w: %s not found and --locked forbids resolving", errLockStale, lockFileName)
		}

		return resolveAndWrite(ctx, res, man, path)
	}

	if readErr != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", lockFileName, readErr)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", lockFileName, err)
	}

	stale, reason, err := res.IsStale(man, lock)
	if err != nil {
		return nil, nil, fmt.Errorf("checking lock staleness: %w", err)
	}

	if !stale {
		return lock, nil, nil
	}

	if locked {
		return nil, nil, exitErrorf(exitStateError, "%w: %s (--locked)", errLockStale, reason)
	}

	return resolveAndWrite(ctx, res, man, path)
}

// resolveAndWrite resolves man into a fresh lock and writes it to path.
func resolveAndWrite(
	ctx context.Context, res resolver, man *manifest.Manifest, path string,
) (*manifest.Lock, []string, error) {
	lock, warnings, err := res.Resolve(ctx, man)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving dependencies: %w", err)
	}

	out, err := lock.Marshal()
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling %s: %w", lockFileName, err)
	}

	writeErr := os.WriteFile(path, out, filePerm)
	if writeErr != nil {
		return nil, nil, fmt.Errorf("writing %s: %w", lockFileName, writeErr)
	}

	return lock, warnings, nil
}
