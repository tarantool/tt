package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
)

// resolver is the slice of resolve.Engine the lock gate drives: report whether
// a lock is stale and (re)resolve a manifest into a fresh lock. *resolve.Engine
// satisfies it; tests substitute a fake so the gate's --locked branching can be
// exercised without a registry.
type resolver interface {
	IsStale(man *manifest.Manifest, lock *manifest.Lock) (bool, string, error)
	ResolvePinned(
		ctx context.Context, man *manifest.Manifest, pins resolve.Pins,
	) (*manifest.Lock, []string, error)
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

		return resolveAndWrite(ctx, res, man, path, nil)
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
		// Naming the command that fixes it matters more here than elsewhere:
		// --locked is what a release pipeline passes, so whoever reads this is
		// looking at a failed build rather than at a shell.
		return nil, nil, exitErrorf(exitStateError,
			"%w: %s (--locked); run tt package resolve to bring the lock back in step",
			errLockStale, reason)
	}

	// Every version the stale lock already chose is held: a manifest edit moves
	// what it touches and what that drags in, and nothing else. Pulling newer
	// versions from the registry is tt package update's job.
	return resolveAndWrite(ctx, res, man, path, resolve.PinsFromLock(lock))
}

// resolveAndWrite resolves man into a fresh lock, preferring pins, and writes
// it to path.
func resolveAndWrite(
	ctx context.Context, res resolver, man *manifest.Manifest, path string, pins resolve.Pins,
) (*manifest.Lock, []string, error) {
	lock, warnings, err := res.ResolvePinned(ctx, man, pins)
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
