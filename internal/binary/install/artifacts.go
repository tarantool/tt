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

// defaultDirPermissions grants write access to the owner and read and execute
// access to everyone.
const defaultDirPermissions = 0o755

// copyBuiltTarantool copies the binary and headers to their installation paths.
func copyBuiltTarantool(binPath, incPath, destBinPath, destIncPath string) error {
	log.Infof("Copying executable...")

	err := copyExecutable(binPath, destBinPath)
	if err != nil {
		return err
	}

	log.Infof("Copying headers...")

	err = copyHeaders(incPath, destIncPath)
	if err != nil {
		return err
	}

	log.Infof("Tarantool executable is installed to: %q", destBinPath)

	return nil
}

// copyExecutable installs an executable with the standard installation permissions.
func copyExecutable(srcPath, destPath string) error {
	err := os.MkdirAll(filepath.Dir(destPath), defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the binary directory: %w", err)
	}

	err = binary.CopyFileChangePerms(srcPath, destPath, defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to copy executable: %w", err)
	}

	return nil
}

// copyHeaders copies available headers and prepares their installation parent.
func copyHeaders(srcPath, destPath string) error {
	err := os.MkdirAll(filepath.Dir(destPath), defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the headers directory: %w", err)
	}

	if srcPath == "" {
		return nil
	}

	if !strings.HasSuffix(srcPath, "/") {
		// copy.Copy expects the directory path to end with '/'.
		srcPath += "/"
	}

	err = copy.Copy(srcPath, destPath+"/")
	if err != nil {
		return fmt.Errorf("failed to copy headers: %w", err)
	}

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
