package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

var errSourceBuildFromBuildConflict = errors.New(
	"--source-build and --from-build cannot be used together")

// prepareTarantoolInstallation installs Tarantool and returns its final activation step.
func prepareTarantoolInstallation(installCtx InstallCtx) (installationActivation, error) {
	log.Infof("Installing tarantool in the %s scope...", installCtx.Scope.String())

	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return nil, err
	}

	if installCtx.FromBuild != "" {
		if installCtx.SourceBuild {
			return nil, errSourceBuildFromBuildConflict
		}

		return prepareTarantoolFromBuild(installCtx, artifactRoot)
	}

	resolved, err := resolveInstallVersion(installCtx)
	if err != nil {
		return nil, err
	}

	selected := binary.Selection{Program: binary.ProgramCe, ID: resolved.name}
	directory := binary.InstallationPath(artifactRoot, selected.Program, selected.ID)
	executable := filepath.Join(directory, selected.Program.Exec())
	headers := filepath.Join(directory, binary.IncDirName, selected.Program.Exec())

	existing, err := prepareExistingProgram(&installCtx, executable, headers, resolved)
	if err != nil {
		return nil, err
	}

	activate := func() error { return activateInstalledVersion(artifactRoot, selected) }
	if existing {
		return activate, nil
	}

	if installCtx.BuildInDocker {
		err = installTarantoolInDocker(resolved.ref, artifactRoot, installCtx)
		if err != nil {
			return nil, err
		}

		return activate, nil
	}

	temporary, cleanup, err := prepareInstallDirectory(installCtx)
	if err != nil {
		return nil, err
	}

	defer cleanup()

	artifact, err := prepareTarantoolArtifact(installCtx, temporary, resolved)
	if err != nil {
		return nil, err
	}

	if installCtx.Reinstall {
		log.Debugf("Removing the installed version...")

		err = os.RemoveAll(directory)
		if err != nil {
			return nil, fmt.Errorf("failed to remove the installed version: %w", err)
		}
	}

	err = copyProgramArtifact(directory, artifact)
	if err != nil {
		return nil, err
	}

	return activate, nil
}

func prepareTarantoolArtifact(installCtx InstallCtx, temporary string,
	resolved resolvedInstallVersion,
) (programArtifact, error) {
	if resolved.kind == versionKindTag && !installCtx.SourceBuild && !installCtx.Dynamic &&
		installCtx.LocalRepo == "" {
		artifact, err := preparePrebuiltTarantool(resolved.ref, temporary, installCtx.KeepTemp)
		if !errors.Is(err, errPrebuiltPackageUnavailable) {
			return artifact, err
		}

		log.Infof("Prebuilt tarantool is unavailable, building from source...")
		log.Debugf("Prebuilt package unavailable: %v", err)
	}

	return prepareTarantoolFromSource(installCtx, temporary, resolved)
}
