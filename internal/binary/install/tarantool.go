package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

var errSourceBuildFromBuildConflict = errors.New(
	"--source-build and --from-build cannot be used together")

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

// installTarantool installs selected version of tarantool.
func installTarantool(installCtx InstallCtx) error {
	log.Infof("Installing tarantool in the %s scope...", installCtx.Scope.String())

	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	if installCtx.FromBuild != "" {
		if installCtx.SourceBuild {
			return errSourceBuildFromBuildConflict
		}

		return installTarantoolFromBuild(installCtx, artifactRoot)
	}

	return installTarantoolVersion(installCtx, artifactRoot)
}

// installTarantoolVersion prepares binary and headers, then installs and activates them.
func installTarantoolVersion(installCtx InstallCtx, artifactRoot string) error {
	resolved, err := resolveInstallVersion(installCtx)
	if err != nil {
		return err
	}

	usePrebuilt := runtime.GOOS == osLinux && resolved.kind == versionKindTag &&
		!installCtx.SourceBuild && !installCtx.Dynamic && !installCtx.BuildInDocker &&
		installCtx.LocalRepo == ""

	versionName := resolved.installName(installCtx.Program)
	versionDir := filepath.Join(artifactRoot, binary.VersionsDirName,
		binary.ProgramCe.String(), versionName)
	dstExecutable := filepath.Join(versionDir, binary.ProgramCe.Exec())
	dstHeaders := filepath.Join(versionDir, binary.IncDirName, binary.ProgramCe.Exec())

	done, err := prepareExistingTarantool(installCtx, dstExecutable, dstHeaders,
		artifactRoot, resolved)
	if err != nil {
		return err
	}

	if done {
		return nil
	}

	if installCtx.BuildInDocker {
		err = installTarantoolInDocker(resolved.ref, artifactRoot, installCtx)
		if err != nil {
			return err
		}

		return activateTarantoolVersion(dstExecutable, dstHeaders, artifactRoot)
	}

	tempDir, err := os.MkdirTemp("", "tarantool_install")
	if err != nil {
		return fmt.Errorf("failed to create a temporary directory: %w", err)
	}

	if installCtx.KeepTemp {
		defer log.Infof("Artifacts can be found at: %s", tempDir)
	} else {
		defer func() { _ = os.RemoveAll(tempDir) }()
	}

	err = os.Chmod(tempDir, defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to set temporary directory permissions: %w", err)
	}

	var artifacts tarantoolArtifacts

	if usePrebuilt {
		artifacts, err = preparePrebuiltTarantool(resolved.ref, tempDir, installCtx.KeepTemp)
		if errors.Is(err, errPrebuiltPackageUnavailable) {
			log.Infof("Prebuilt Tarantool is unavailable, building from source...")
			log.Debugf("Prebuilt package unavailable: %v", err)

			artifacts, err = prepareTarantoolFromSource(installCtx, tempDir, resolved)
		}
	} else {
		artifacts, err = prepareTarantoolFromSource(installCtx, tempDir, resolved)
	}

	if err != nil {
		return err
	}

	if installCtx.Reinstall {
		log.Debugf("Removing the installed version...")

		err = os.RemoveAll(versionDir)
		if err != nil {
			return fmt.Errorf("failed to remove the installed version: %w", err)
		}
	}

	err = copyTarantoolArtifacts(artifacts, dstExecutable, dstHeaders)
	if err != nil {
		return err
	}

	err = activateTarantoolVersion(dstExecutable, dstHeaders, artifactRoot)
	if err != nil {
		return err
	}

	return nil
}

// activateTarantoolVersion updates the scope's active links.
func activateTarantoolVersion(binPath, incPath, artifactRoot string) error {
	log.Debugf("Changing symlinks...")

	err := changeActiveTarantoolVersion(binPath, incPath, artifactRoot)
	if err != nil {
		return err
	}

	binDir := filepath.Join(artifactRoot, binary.BinDirName)
	log.Infof("Made default by symlink %q", filepath.Join(binDir, binary.ProgramCe.Exec()))
	log.Infof("Done.")

	return nil
}
