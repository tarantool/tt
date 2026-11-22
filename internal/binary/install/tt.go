package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/v3/log"
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

	selected := binary.Selection{
		Program: binary.ProgramTt, ID: ttInstallationID(artifactRoot, resolved),
	}
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

// ttInstallationID returns the installation of the tt resolved in root: the
// name of the version, or, for a release installed before as tt_v<version>
// and not as tt_<version>, that older name, so the release is not
// installed twice.
func ttInstallationID(root string, resolved resolvedInstallVersion) string {
	if resolved.kind != versionKindTag {
		return resolved.name
	}

	installed := func(selected string) bool {
		return binary.IsRegularFile(filepath.Join(
			binary.InstallationPath(root, binary.ProgramTt, selected), binary.ProgramTt.Exec()))
	}

	legacy := "v" + resolved.name
	if !installed(resolved.name) && installed(legacy) {
		return legacy
	}

	return resolved.name
}

func prepareTtArtifact(installCtx InstallCtx, temporary string,
	resolved resolvedInstallVersion,
) (programArtifact, error) {
	if resolved.kind == versionKindTag && takesPrebuilt(installCtx) {
		artifact, err := preparePrebuilt(context.Background(), binary.ProgramTt, resolved.ref,
			temporary, installCtx.KeepTemp)
		if !errors.Is(err, errPrebuiltPackageUnavailable) {
			return artifact, err
		}

		log.Infof("Prebuilt tt is unavailable, building from source...")
		log.Debugf("Prebuilt package unavailable: %v", err)
	}

	return prepareTtFromSource(installCtx, temporary, resolved)
}
