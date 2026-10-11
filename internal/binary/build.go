package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TarantoolBuildPath returns the executable path of a linked local build, or an
// empty string when Tarantool is not a symlink pointing outside binDir.
// It also recognizes dangling links so that removed builds can be listed and unlinked.
func TarantoolBuildPath(binDir string) (string, error) {
	binDir, err := filepath.Abs(binDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	link := filepath.Join(binDir, tarantoolName)
	info, err := os.Lstat(link)

	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("failed to access %q: %w", link, err)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return "", nil
	}

	bin, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("failed to read symlink: %w", err)
	}

	if !filepath.IsAbs(bin) {
		bin = filepath.Join(binDir, bin)
	}

	if filepath.Dir(bin) == binDir {
		return "", nil
	}

	return bin, nil
}
