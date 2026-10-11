package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
)

// copyBuildedTT copies tt binary.
func copyBuildedTT(binDir, path, version string, installCtx InstallCtx) error {
	_, err := os.Stat(binDir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(binDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("unable to create %s\n Error: %w", binDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("unable to create %s\n Error: %w", binDir, err)
	}

	if installCtx.Reinstall {
		err = os.Remove(filepath.Join(binDir, version))
		if err != nil {
			return fmt.Errorf("failed to remove the installed version: %w", err)
		}
	}

	binPathTt := filepath.Join(binDir, version)

	err = binary.CopyFilePreserve(filepath.Join(path, "tt"), binPathTt)
	if err != nil {
		return err
	}

	log.Infof("Tt executable is installed to: %q", binPathTt)

	return nil
}

func prepareExistingTt(installCtx *InstallCtx, binDir, versionStr, ttVersion,
	distfiles string,
) (bool, error) {
	if installCtx.Reinstall {
		return false, nil
	}

	log.Infof("Checking existing...")

	pathToBin := filepath.Join(binDir, versionStr)
	if !binary.IsRegularFile(pathToBin) {
		return false, nil
	}

	isBinExecutable, err := binary.IsExecOwner(pathToBin)
	if err != nil {
		return false, err
	}

	updatePossible, err := isUpdatePossible(*installCtx, pathToBin, binary.ProgramTt,
		ttVersion, distfiles, isBinExecutable)
	if err != nil {
		return false, err
	}

	if updatePossible {
		log.Info("Found newest commit of tt in master")

		// Reduce the case to a reinstallation.
		installCtx.Reinstall = true

		return false, nil
	}

	log.Infof("%s version of tt already exists, updating symlink...", versionStr)

	err = binary.CreateSymlink(versionStr, filepath.Join(binDir, binary.ProgramTt.Exec()), true)
	if err != nil {
		return false, err
	}

	log.Infof("Done")

	return true, nil
}

func downloadTtSource(path, ttVersion, distfiles string, resolved resolvedInstallVersion,
	installCtx InstallCtx, logFile *os.File,
) error {
	if installCtx.Local {
		if !binary.IsDir(filepath.Join(distfiles, "tt")) {
			return errDistfilesDirectoryNotFound
		}

		log.Infof("Local files found, installing from them...")

		localPath, _ := binary.JoinAbspath(distfiles, "tt")

		err := copy.Copy(localPath, path)
		if err != nil {
			return fmt.Errorf("failed to copy local tt sources: %w", err)
		}

		err = gitCheckout(path, ttVersion, installCtx.Verbose, logFile)
		if err != nil {
			_ = printLog(logFile.Name())
		}

		return err
	}

	log.Infof("Downloading tt...")

	if resolved.versionFound {
		err := downloadRepo(search.GitRepoTT, ttVersion, path, logFile, installCtx.Verbose)
		if err != nil {
			_ = printLog(logFile.Name())
		}

		return err
	}

	err := downloadRepo(search.GitRepoTT, masterVersion, path, logFile, installCtx.Verbose)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	if resolved.isPullRequest {
		pullRequestCommand := "pull/" + resolved.pullRequestID + "/head:" + ttVersion

		err = binary.ExecuteCommand(gitExec, installCtx.Verbose, logFile, path,
			"fetch", "origin", pullRequestCommand)
		if err != nil {
			_ = printLog(logFile.Name())
			return err
		}
	}

	err = gitCheckout(path, ttVersion, installCtx.Verbose, logFile)
	if err != nil {
		_ = printLog(logFile.Name())
	}

	return err
}

// installTt installs selected version of tt.
func installTt(installCtx InstallCtx, cliOpts *config.CliOpts) error {
	err := prepareInstallDirs(&installCtx, cliOpts)
	if err != nil {
		return err
	}

	binDir := cliOpts.Env.BinDir
	distfiles := cliOpts.Repo.Install

	resolved, err := resolveTtInstallVersion(installCtx, distfiles)
	if err != nil {
		return err
	}

	ttVersion := resolved.version
	versionStr := resolved.versionStr
	isPullRequest := resolved.isPullRequest
	pullRequestHash := resolved.pullRequestHash

	// Check if that version is already installed.
	// If it is installed, check if the newest version exists.
	done, err := prepareExistingTt(&installCtx, binDir, versionStr, ttVersion, distfiles)
	if err != nil {
		return err
	}

	if done {
		return nil
	}

	// Check binary directory.
	if binDir == "" {
		return fmt.Errorf("%w%s", errBinDirNotSet, configure.ConfigName)
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	defer func() {
		_ = os.Remove(logFile.Name())
	}()

	log.Infof("Installing tt=" + ttVersion)

	// Check tt dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")

		err = programDependenciesInstalled(binary.ProgramTt)
		if err != nil {
			return err
		}
	}

	path, err := os.MkdirTemp("", "tt_install")
	if err != nil {
		return fmt.Errorf("failed to create a temporary directory: %w", err)
	}

	_ = os.Chmod(path, defaultDirPermissions)

	if !installCtx.KeepTemp {
		defer func() {
			_ = os.RemoveAll(path)
		}()
	}

	// Download tt.
	err = downloadTtSource(path, ttVersion, distfiles, resolved, installCtx, logFile)
	if err != nil {
		return err
	}

	// Build tt.
	log.Infof("Building tt...")

	err = binary.ExecuteCommand(mageExec, installCtx.Verbose, logFile, path,
		"build")
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	if isPullRequest {
		log.Infof("Binary name is %s", pullRequestHash)
	}

	// Copy binary.
	log.Infof("Copying executable...")

	err = copyBuildedTT(binDir, path, versionStr, installCtx)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	// Set symlink.
	symlinkPath := filepath.Join(binDir, "tt")

	err = binary.CreateSymlink(versionStr, symlinkPath, true)
	if err != nil {
		_ = printLog(logFile.Name())
		return err
	}

	log.Infof("Made default by symlink %q", symlinkPath)
	log.Info("Use the following command to add the bin_dir directory to the PATH: . <(tt env)")
	log.Infof("Done.")

	if installCtx.KeepTemp {
		log.Infof("Artifacts can be found at: %s", path)
	}

	return nil
}
