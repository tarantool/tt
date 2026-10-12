package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/cli/version"
)

// TarantoolBuildPath returns the target of a local build linked from binDir.
// Links within binDir or to this scope's installed versions return an empty path.
// Dangling links are classified the same way as links to existing executables.
func TarantoolBuildPath(binDir string) (string, error) {
	binDir, err := filepath.Abs(binDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	link := filepath.Join(binDir, tarantoolName)
	info, err := os.Lstat(link)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("failed to access %q: %w", link, err)
	case info.Mode()&os.ModeSymlink == 0:
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

	installationRoot := tarantoolInstallationRoot(bin)
	if installationRoot == "" {
		return bin, nil
	}

	root := filepath.Dir(binDir)

	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("failed to access scope %q: %w", root, err)
	}

	// Compare roots so scope aliases and removed version directories still work.
	targetInfo, err := os.Stat(installationRoot)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return bin, nil
	case err != nil:
		return "", fmt.Errorf("failed to access scope %q: %w", installationRoot, err)
	case os.SameFile(rootInfo, targetInfo):
		return "", nil
	default:
		return bin, nil
	}
}

// tarantoolInstallationRoot recognizes versions/<product>/<product>_<version>/tarantool.
func tarantoolInstallationRoot(target string) string {
	installation := filepath.Dir(target)
	productDir := filepath.Dir(installation)
	versionsDir := filepath.Dir(productDir)

	if filepath.Base(target) != ProgramCe.Exec() || filepath.Base(versionsDir) != VersionsDirName {
		return ""
	}

	product := filepath.Base(productDir)

	switch product {
	case ProgramCe.String(), ProgramEe.String():
		name, found := strings.CutPrefix(filepath.Base(installation), product+version.FsSeparator)
		if found && name != "" {
			return filepath.Dir(versionsDir)
		}
	}

	return ""
}
