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

// programArtifact is one prepared program, independent of its source and installation scope.
// An SDK is a collection of these artifacts, one per included program.
type programArtifact struct {
	program    binary.Program
	executable string
	headers    string
}

// copyProgramArtifact copies a program's executable and optional headers into a version directory.
// The same layout is used for final installation directories and staged bundle directories.
func copyProgramArtifact(directory string, artifact programArtifact) error {
	log.Debugf("Copying executable...")

	err := copyExecutable(artifact.executable, filepath.Join(directory, artifact.program.Exec()))
	if err != nil {
		return err
	}

	if artifact.headers != "" {
		log.Debugf("Copying headers...")

		err = copyHeaders(artifact.headers,
			filepath.Join(directory, binary.IncDirName, artifact.program.Exec()))
		if err != nil {
			return err
		}
	}

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

func activateInstalledVersion(root string, selected binary.Selection) error {
	log.Debugf("Changing symlinks...")

	err := binary.ValidateInstallation(root, selected)
	if err != nil {
		return err
	}

	err = binary.ActivateSelections(root, []binary.Selection{selected})
	if err != nil {
		return err
	}

	log.Infof("%s executable is installed to: %q", selected.Program,
		filepath.Join(binary.InstallationPath(root, selected.Program, selected.ID),
			selected.Program.Exec()))
	log.Infof("Made default by symlink %q", filepath.Join(root, binary.BinDirName,
		selected.Program.Exec()))
	log.Infof("Done.")

	return nil
}
