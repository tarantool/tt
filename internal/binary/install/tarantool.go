package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errIncDirIsNotSetCheck = errors.New("inc_dir is not set, check ")
)

func prepareTarantoolInstall(binDir, incDir string, installCtx InstallCtx) (
	resolvedInstallVersion,
	error,
) {
	if binDir == "" {
		return resolvedInstallVersion{},
			fmt.Errorf("%w%s", errBinDirNotSet, configure.ConfigName)
	}

	if incDir == "" {
		return resolvedInstallVersion{},
			fmt.Errorf("%w%s", errIncDirIsNotSetCheck, configure.ConfigName)
	}

	return resolveInstallVersion(installCtx)
}

func prepareExistingTarantool(installCtx InstallCtx, binDir, incDir, versionStr string,
	kind versionKind,
) (bool, error) {
	if installCtx.Reinstall {
		return false, nil
	}

	log.Infof("Checking existing...")

	pathToBin := filepath.Join(binDir, versionStr)
	if !binary.IsRegularFile(pathToBin) || !binary.IsDir(filepath.Join(incDir, versionStr)) {
		return false, nil
	}

	isBinExecutable, err := binary.IsExecOwner(pathToBin)
	if err != nil {
		return false, err
	}

	updatePossible, err := isUpdatePossible(installCtx, pathToBin, kind, isBinExecutable)
	if err != nil {
		return false, err
	}

	if updatePossible {
		log.Infof("Found newest commit of tarantool in master")

		return false, nil
	}

	log.Infof("%s version of tarantool already exists, updating symlinks...", versionStr)

	err = changeActiveTarantoolVersion(versionStr, binDir, incDir)
	if err != nil {
		return false, err
	}

	log.Infof("Done")

	return true, nil
}

func downloadTarantoolSource(installCtx InstallCtx, path string,
	resolved resolvedInstallVersion, logFile *os.File,
) error {
	if installCtx.Local {
		log.Infof("Checking local files...")

		localRepoPath := filepath.Join(installCtx.localRepoDir, installCtx.Program.String())

		return copyLocalTarantool(localRepoPath, path, resolved.ref,
			installCtx.Verbose, logFile)
	}

	log.Infof("Downloading tarantool...")

	return downloadGitVersion(binary.ProgramCe.GitRepo(), path, resolved,
		installCtx.Verbose, logFile)
}

// installTarantool installs selected version of tarantool.
func installTarantool(installCtx InstallCtx, cliOpts *config.CliOpts) error {
	err := prepareInstallDirs(&installCtx, cliOpts)
	if err != nil {
		return err
	}

	if installCtx.FromBuild != "" {
		return installTarantoolFromBuild(installCtx, cliOpts)
	}

	return installTarantoolFromSource(installCtx, cliOpts)
}

// installTarantoolFromSource builds and installs Tarantool from source.
func installTarantoolFromSource(installCtx InstallCtx, cliOpts *config.CliOpts) error {
	binDir := cliOpts.Env.BinDir
	incDir := installCtx.IncDir

	resolved, err := prepareTarantoolInstall(binDir, incDir, installCtx)
	if err != nil {
		return err
	}

	tarVersion := resolved.ref
	versionStr := resolved.installName(installCtx.Program)

	// Check if program is already installed.
	// If it is installed, check if newest version exists.
	done, err := prepareExistingTarantool(
		installCtx, binDir, incDir, versionStr, resolved.kind)
	if err != nil {
		return err
	}

	if done {
		return nil
	}

	if installCtx.BuildInDocker {
		return installTarantoolInDocker(tarVersion, binDir, incDir, installCtx)
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
	if installCtx.Reinstall && binary.IsRegularFile(filepath.Join(binDir, versionStr)) {
		log.Infof("%s version of tarantool already exists, removing files...",
			versionStr)

		err = os.RemoveAll(filepath.Join(binDir, versionStr))
		if err != nil {
			_ = printLog(logFile.Name())
			return fmt.Errorf("failed to remove the installed executable: %w", err)
		}

		err = os.RemoveAll(filepath.Join(incDir, versionStr))
		if err != nil {
			_ = printLog(logFile.Name())
			return fmt.Errorf("failed to remove the installed headers: %w", err)
		}
	}

	binPath := filepath.Join(buildPath, "tarantool-prefix", "bin", "tarantool")
	incPath := filepath.Join(buildPath, "tarantool-prefix", "include", "tarantool")

	err = copyBuiltTarantool(binPath, incPath, filepath.Join(binDir, versionStr),
		filepath.Join(incDir, versionStr))
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	// Set symlinks.
	log.Infof("Changing symlinks...")

	err = changeActiveTarantoolVersion(versionStr, binDir, incDir)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	log.Infof("Made default by symlink %q", filepath.Join(incDir, "tarantool"))
	log.Info("Use the following command to add the bin_dir directory to the PATH: . <(tt env)")
	log.Infof("Done.")

	if installCtx.KeepTemp {
		log.Infof("Artifacts can be found at: %s", path)
	}

	return nil
}
