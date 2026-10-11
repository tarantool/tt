package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

// copyBuildedTarantool copies binary and include dir.
func copyBuildedTarantool(binPath, incPath, binDir, includeDir, version string) error {
	log.Infof("Copying executable...")

	_, err := os.Stat(binDir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(binDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("unable to create %s\n Error: %w", binDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("unable to create %s\n Error: %w", binDir, err)
	}

	execPath := filepath.Join(binDir, version)

	err = binary.CopyFileChangePerms(binPath, execPath, defaultDirPermissions)
	if err != nil {
		return err
	}

	log.Infof("Copying headers...")

	_, err = os.Stat(includeDir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(includeDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("unable to create %s\n Error: %w", includeDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("unable to create %s\n Error: %w", includeDir, err)
	}

	if incPath != "" {
		if !strings.HasSuffix(incPath, "/") {
			// Note: copy.Copy expects the directory path to end with '/'.
			incPath += "/"
		}

		err = copy.Copy(incPath, filepath.Join(includeDir, version)+"/")
		if err != nil {
			return fmt.Errorf("failed to copy headers: %w", err)
		}
	}

	log.Infof("Tarantool executable is installed to: %q", execPath)

	return nil
}

// changeActiveTarantoolVersion changes symlinks to the specified tarantool version.
func changeActiveTarantoolVersion(versionStr, binDir, incDir string) error {
	err := binary.CreateSymlink(versionStr, filepath.Join(binDir, "tarantool"), true)
	if err != nil {
		return err
	}

	err = binary.CreateSymlink(versionStr, filepath.Join(incDir, "tarantool"), true)

	return err
}
