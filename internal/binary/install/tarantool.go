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
	"github.com/tarantool/tt/v3/internal/binary/search"
)

var (
	errIncDirIsNotSetCheck = errors.New("inc_dir is not set, check ")
)

func prepareTarantoolInstall(binDir, incDir string, installCtx InstallCtx, distfiles string) (
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

	return resolveTarantoolInstallVersion(installCtx, distfiles)
}

func prepareExistingTarantool(installCtx InstallCtx, binDir, incDir, versionStr,
	tarVersion, distfiles string,
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

	updatePossible, err := isUpdatePossible(installCtx, pathToBin, binary.ProgramCe,
		tarVersion, distfiles, isBinExecutable)
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

func downloadTarantoolSource(path, tarVersion, distfiles string,
	resolved resolvedInstallVersion, installCtx InstallCtx, logFile *os.File,
) error {
	if installCtx.Local {
		log.Infof("Checking local files...")

		return copyLocalTarantool(distfiles, path, tarVersion, installCtx, logFile)
	}

	log.Infof("Downloading tarantool...")

	if resolved.versionFound {
		return downloadRepo(search.GitRepoTarantool, tarVersion, path, logFile,
			installCtx.Verbose)
	}

	err := downloadRepo(search.GitRepoTarantool, masterVersion, path, logFile,
		installCtx.Verbose)
	if err != nil {
		return err
	}

	if resolved.isPullRequest {
		pullRequestCommand := "pull/" + resolved.pullRequestID + "/head:" + tarVersion

		err = binary.ExecuteCommand(gitExec, installCtx.Verbose, logFile, path,
			"fetch", "origin", pullRequestCommand)
		if err != nil {
			return err
		}
	}

	return gitCheckout(path, tarVersion, installCtx.Verbose, logFile)
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
	distfiles := cliOpts.Repo.Install

	resolved, err := prepareTarantoolInstall(binDir, incDir, installCtx, distfiles)
	if err != nil {
		return err
	}

	tarVersion := resolved.version
	versionStr := resolved.versionStr
	isPullRequest := resolved.isPullRequest
	pullRequestHash := resolved.pullRequestHash

	// Check if program is already installed.
	// If it is installed, check if newest version exists.
	done, err := prepareExistingTarantool(
		installCtx, binDir, incDir, versionStr, tarVersion, distfiles)
	if err != nil {
		return err
	}

	if done {
		return nil
	}

	if installCtx.BuildInDocker {
		return installTarantoolInDocker(tarVersion, binDir, incDir, installCtx, distfiles)
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
	err = downloadTarantoolSource(path, tarVersion, distfiles, resolved, installCtx, logFile)
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

	if isPullRequest {
		log.Infof("Binary name is %s", pullRequestHash)
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

	err = copyBuildedTarantool(binPath, incPath, binDir, incDir, versionStr)
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
