package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/v3/log"
	"github.com/tarantool/tt/v3/cli/version"
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

	warnDevelopmentTag(resolved)

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

// warnDevelopmentTag warns that a tag such as 3.5.0-entrypoint, which tt
// binary search leaves out, is no release: it has no prebuilt build and is
// built from source, which takes minutes.
func warnDevelopmentTag(resolved resolvedInstallVersion) {
	if resolved.kind != versionKindTag {
		return
	}

	parsed, err := version.Parse(resolved.ref)
	if err != nil || parsed.Release.Type != version.TypeNightly || parsed.Release.String() == "" {
		return
	}

	log.Warnf("%s is a development tag, not a release: it is built from source; "+
		"tt binary search tarantool lists the releases", resolved.ref)
}

func prepareTarantoolArtifact(installCtx InstallCtx, temporary string,
	resolved resolvedInstallVersion,
) (programArtifact, error) {
	if resolved.kind == versionKindTag && takesPrebuilt(installCtx) {
		artifact, err := preparePrebuilt(context.Background(), binary.ProgramCe, resolved.ref,
			temporary, installCtx.KeepTemp)
		if !errors.Is(err, errPrebuiltPackageUnavailable) {
			return artifact, err
		}

		log.Infof("Prebuilt tarantool is unavailable, building from source...")
		log.Debugf("Prebuilt package unavailable: %v", err)
	}

	return prepareTarantoolFromSource(installCtx, temporary, resolved)
}
