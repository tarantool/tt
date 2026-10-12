package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

// copyBuiltTT copies the built binary into the version's bin directory.
func copyBuiltTT(binPath, buildPath string, reinstall bool) error {
	err := os.MkdirAll(filepath.Dir(binPath), defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the version directory: %w", err)
	}

	if reinstall {
		err = os.Remove(binPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove the installed version: %w", err)
		}
	}

	err = binary.CopyFilePreserve(filepath.Join(buildPath, binary.ProgramTt.Exec()), binPath)
	if err != nil {
		return err
	}

	log.Infof("Tt executable is installed to: %q", binPath)

	return nil
}

// changeActiveTtVersion points the scope's tt executable at the selected version.
func changeActiveTtVersion(binPath, symlinkPath string) error {
	err := os.MkdirAll(filepath.Dir(symlinkPath), defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create the binary directory: %w", err)
	}

	return binary.CreateSymlink(binPath, symlinkPath, true)
}

func prepareExistingTt(installCtx *InstallCtx, pathToBin, symlinkPath string,
	resolved resolvedInstallVersion,
) (bool, error) {
	if installCtx.Reinstall {
		return false, nil
	}

	log.Infof("Checking existing...")

	if !binary.IsRegularFile(pathToBin) {
		return false, nil
	}

	isBinExecutable, err := binary.IsExecOwner(pathToBin)
	if err != nil {
		return false, err
	}

	updatePossible, err := isUpdatePossible(*installCtx, pathToBin, resolved.kind,
		isBinExecutable)
	if err != nil {
		return false, err
	}

	if updatePossible {
		log.Info("Found newest commit of tt in master")

		// Reduce the case to a reinstallation.
		installCtx.Reinstall = true

		return false, nil
	}

	log.Infof("%s version of tt already exists, updating symlink...", resolved.name)

	err = changeActiveTtVersion(pathToBin, symlinkPath)
	if err != nil {
		return false, err
	}

	log.Infof("Done")

	return true, nil
}

// installTt installs selected version of tt.
func installTt(installCtx InstallCtx) error {
	log.Infof("Installing tt in the %s scope...", installCtx.Scope.String())

	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	resolved, err := resolveInstallVersion(installCtx)
	if err != nil {
		return err
	}

	versionStr := resolved.installName(installCtx.Program)

	// Destination of this version's executable: <scope>/versions/tt/<version>/tt.
	binPath := filepath.Join(artifactRoot, binary.VersionsDirName, binary.ProgramTt.String(),
		versionStr, binary.ProgramTt.Exec())

	// Directory of active executable symlinks: <scope>/bin, intended for PATH.
	binDir := filepath.Join(artifactRoot, binary.BinDirName)

	// Symlink selecting the active tt version: <scope>/bin/tt points to binPath.
	symlinkPath := filepath.Join(binDir, binary.ProgramTt.Exec())

	// Check if that version is already installed.
	// If it is installed, check if the newest version exists.
	done, err := prepareExistingTt(&installCtx, binPath, symlinkPath, resolved)
	if err != nil {
		return err
	}

	if done {
		return nil
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	defer func() {
		_ = os.Remove(logFile.Name())
	}()

	log.Infof("Installing tt=" + resolved.ref)

	// Check tt dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")

		err = programDependenciesInstalled(binary.ProgramTt)
		if err != nil {
			return err
		}
	}

	tempBuildPath, err := os.MkdirTemp("", "tt_install")
	if err != nil {
		return fmt.Errorf("failed to create a temporary directory: %w", err)
	}

	_ = os.Chmod(tempBuildPath, defaultDirPermissions)

	if !installCtx.KeepTemp {
		defer func() {
			_ = os.RemoveAll(tempBuildPath)
		}()
	}

	// Download tt.
	err = downloadTtSource(installCtx, tempBuildPath, resolved, logFile)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	// Build tt.
	log.Infof("Building tt...")

	err = binary.ExecuteCommand(mageExec, installCtx.Verbose, logFile, tempBuildPath,
		"build")
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	if resolved.kind == versionKindPullRequest {
		log.Infof("Binary name is %s", resolved.name)
	}

	// Copy binary.
	log.Infof("Copying executable...")

	err = copyBuiltTT(binPath, tempBuildPath, installCtx.Reinstall)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	// Set symlink.
	err = changeActiveTtVersion(binPath, symlinkPath)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	log.Infof("Made default by symlink %q", symlinkPath)
	log.Infof("Add %q to PATH to use this as default.", binDir)
	log.Infof("Done.")

	if installCtx.KeepTemp {
		log.Infof("Artifacts can be found at: %s", tempBuildPath)
	}

	return nil
}
