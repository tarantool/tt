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

// copyBuiltTarantool copies the binary and headers to their installation paths.
func copyBuiltTarantool(binPath, incPath, destBinPath, destIncPath string) error {
	log.Infof("Copying executable...")

	err := os.MkdirAll(filepath.Dir(destBinPath), defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the binary directory: %w", err)
	}

	err = binary.CopyFileChangePerms(binPath, destBinPath, defaultDirPermissions)
	if err != nil {
		return err
	}

	log.Infof("Copying headers...")

	err = os.MkdirAll(filepath.Dir(destIncPath), defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the headers directory: %w", err)
	}

	if incPath != "" {
		if !strings.HasSuffix(incPath, "/") {
			// Note: copy.Copy expects the directory path to end with '/'.
			incPath += "/"
		}

		err = copy.Copy(incPath, destIncPath+"/")
		if err != nil {
			return fmt.Errorf("failed to copy headers: %w", err)
		}
	}

	log.Infof("Tarantool executable is installed to: %q", destBinPath)

	return nil
}

// changeActiveTarantoolVersion selects the binary and headers in the installation scope.
func changeActiveTarantoolVersion(binPath, incPath, artifactRoot string) error {
	binDir := filepath.Join(artifactRoot, binary.BinDirName)
	incDir := filepath.Join(artifactRoot, binary.IncDirName)

	err := os.MkdirAll(binDir, defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the binary directory: %w", err)
	}

	err = os.MkdirAll(incDir, defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the headers directory: %w", err)
	}

	err = binary.CreateSymlink(binPath, filepath.Join(binDir, binary.ProgramCe.Exec()), true)
	if err != nil {
		return err
	}

	err = binary.CreateSymlink(incPath, filepath.Join(incDir, binary.ProgramCe.Exec()), true)

	return err
}
