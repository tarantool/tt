package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

// prepareTtInstallation installs tt and returns its final activation step.
func prepareTtInstallation(installCtx InstallCtx) (installationActivation, error) {
	log.Infof("Installing tt in the %s scope...", installCtx.Scope.String())

	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return nil, err
	}

	resolved, err := resolveInstallVersion(installCtx)
	if err != nil {
		return nil, err
	}

	selected := binary.Selection{Program: binary.ProgramTt, ID: resolved.name}
	directory := binary.InstallationPath(artifactRoot, selected.Program, selected.ID)
	executable := filepath.Join(directory, selected.Program.Exec())

	existing, err := prepareExistingProgram(&installCtx, executable, "", resolved)
	if err != nil {
		return nil, err
	}

	activate := func() error { return activateInstalledVersion(artifactRoot, selected) }
	if existing {
		return activate, nil
	}

	temporary, cleanup, err := prepareInstallDirectory(installCtx)
	if err != nil {
		return nil, err
	}

	defer cleanup()

	artifact, err := prepareTtArtifact(installCtx, temporary, resolved)
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

func prepareTtArtifact(installCtx InstallCtx, temporary string,
	resolved resolvedInstallVersion,
) (programArtifact, error) {
	if resolved.kind == versionKindTag && !installCtx.SourceBuild && installCtx.LocalRepo == "" {
		artifact, err := preparePrebuiltTt(resolved.ref, temporary, installCtx.KeepTemp)
		if !errors.Is(err, errPrebuiltPackageUnavailable) {
			return artifact, err
		}

		log.Infof("Prebuilt tt is unavailable, building from source...")
		log.Debugf("Prebuilt package unavailable: %v", err)
	}

	return prepareTtFromSource(installCtx, temporary, resolved)
}
