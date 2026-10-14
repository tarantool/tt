package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

func prepareExistingTarantool(installCtx InstallCtx, binPath, incPath, artifactRoot string,
	resolved resolvedInstallVersion,
) (bool, error) {
	if installCtx.Reinstall {
		return false, nil
	}

	log.Infof("Checking existing...")

	if !binary.IsRegularFile(binPath) || !binary.IsDir(incPath) {
		return false, nil
	}

	isBinExecutable, err := binary.IsExecOwner(binPath)
	if err != nil {
		return false, err
	}

	updatePossible, err := isUpdatePossible(installCtx, binPath, resolved.kind,
		isBinExecutable)
	if err != nil {
		return false, err
	}

	if updatePossible {
		log.Infof("Found newest commit of tarantool in master")

		return false, nil
	}

	log.Infof("%s version of tarantool already exists, updating symlinks...",
		resolved.installName(installCtx.Program))

	err = changeActiveTarantoolVersion(binPath, incPath, artifactRoot)
	if err != nil {
		return false, err
	}

	log.Infof("Done")

	return true, nil
}

func downloadTarantoolSource(installCtx InstallCtx, path string,
	resolved resolvedInstallVersion, logFile *os.File,
) error {
	if installCtx.LocalRepo != "" {
		log.Infof("Checking local files...")

		localRepoPath := filepath.Join(installCtx.LocalRepo, installCtx.Program.String())

		return copyLocalTarantool(localRepoPath, path, resolved.ref,
			installCtx.Verbose, logFile)
	}

	log.Infof("Downloading tarantool...")

	return downloadGitVersion(installCtx.Program.GitRepo(), path, resolved,
		installCtx.Verbose, logFile)
}

// installTarantool installs selected version of tarantool.
func installTarantool(installCtx InstallCtx) error {
	log.Infof("Installing tarantool in the %s scope...", installCtx.Scope.String())

	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	if installCtx.FromBuild != "" {
		return installTarantoolFromBuild(installCtx, artifactRoot)
	}

	return installTarantoolFromSource(installCtx, artifactRoot)
}

// installTarantoolFromSource builds and installs Tarantool from source.
func installTarantoolFromSource(installCtx InstallCtx, artifactRoot string) error {
	resolved, err := resolveInstallVersion(installCtx)
	if err != nil {
		return err
	}

	tarVersion := resolved.ref
	versionStr := resolved.installName(installCtx.Program)

	// Each version owns its binary and headers under <scope>/versions/tarantool/<version>.
	versionDir := filepath.Join(artifactRoot, binary.VersionsDirName,
		binary.ProgramCe.String(), versionStr)

	// Destination of this version's executable: <scope>/versions/tarantool/<version>/tarantool.
	binPath := filepath.Join(versionDir, binary.ProgramCe.Exec())

	// This version's headers: <scope>/versions/tarantool/<version>/include/tarantool.
	incPath := filepath.Join(versionDir, binary.IncDirName, binary.ProgramCe.Exec())

	// Check if program is already installed.
	// If it is installed, check if newest version exists.
	done, err := prepareExistingTarantool(
		installCtx, binPath, incPath, artifactRoot, resolved)
	if err != nil {
		return err
	}

	if done {
		return nil
	}

	if installCtx.BuildInDocker {
		err = installTarantoolInDocker(tarVersion, artifactRoot, installCtx)
		if err != nil {
			return err
		}

		return activateTarantoolVersion(binPath, incPath, artifactRoot)
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	defer func() {
		_ = os.Remove(logFile.Name())
	}()

	log.Infof("Installing tarantool=" + tarVersion)

	// Check dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")

		err = programDependenciesInstalled(binary.ProgramCe)
		if err != nil {
			return err
		}
	}

	path, err := os.MkdirTemp("", "tarantool_install")
	if err != nil {
		return fmt.Errorf("failed to create a temporary directory: %w", err)
	}

	_ = os.Chmod(path, defaultDirPermissions)

	if !installCtx.KeepTemp {
		defer func() {
			_ = os.RemoveAll(path)
		}()
	}

	// Download tarantool.
	err = downloadTarantoolSource(installCtx, path, resolved, logFile)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	// Build tarantool.
	log.Infof("Building tarantool...")

	buildPath, err := buildTarantool(path, installCtx, logFile)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	if resolved.kind == versionKindPullRequest {
		log.Infof("Binary name is %s", resolved.name)
	}

	// Copy binary and headers.
	if installCtx.Reinstall {
		log.Infof("%s version of tarantool already exists, removing files...",
			versionStr)

		err = os.RemoveAll(versionDir)
		if err != nil {
			_ = printLog(logFile.Name())
			return fmt.Errorf("failed to remove the installed version: %w", err)
		}
	}

	builtBinPath := filepath.Join(buildPath, "tarantool-prefix", "bin", "tarantool")
	builtIncPath := filepath.Join(buildPath, "tarantool-prefix", "include", "tarantool")

	err = copyBuiltTarantool(builtBinPath, builtIncPath, binPath, incPath)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	err = activateTarantoolVersion(binPath, incPath, artifactRoot)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	if installCtx.KeepTemp {
		log.Infof("Artifacts can be found at: %s", path)
	}

	return nil
}

// activateTarantoolVersion updates the scope's active links and reports its PATH directory.
func activateTarantoolVersion(binPath, incPath, artifactRoot string) error {
	log.Infof("Changing symlinks...")

	err := changeActiveTarantoolVersion(binPath, incPath, artifactRoot)
	if err != nil {
		return err
	}

	binDir := filepath.Join(artifactRoot, binary.BinDirName)
	log.Infof("Made default by symlink %q", filepath.Join(binDir, binary.ProgramCe.Exec()))
	log.Infof("Add %q to PATH to use this as default.", binDir)
	log.Infof("Done.")

	return nil
}
