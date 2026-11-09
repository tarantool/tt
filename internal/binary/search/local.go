package search

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/customerzone"
)

var (
	errLocalRepoRequired                 = errors.New("local repository directory is required")
	errLocalSearchNotSupportedForProgram = errors.New("local search not supported for program '")
)

// searchVersionsLocalGit handles searching versions from a local Git repository clone.
// It returns a slice of versions found.
func searchVersionsLocalGit(program binary.Program, repoPath string) (
	version.VersionSlice, error,
) {
	_, err := os.Stat(repoPath)
	if os.IsNotExist(err) {
		// It's not an error if the local repo doesn't exist.
		log.Debugf("Local repository for %s not found at %s", program, repoPath)

		return nil, nil
	}

	versions, err := GetVersionsFromGitLocal(repoPath, program)
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from local repo %s: %w", repoPath, err)
	}

	return append(versions, newMasterVersion()), nil
}

// searchVersionsLocalBundles searches versions from locally available archives.
// It returns a slice of versions found.
func searchVersionsLocalBundles(searchCtx *SearchCtx, dir string) (
	version.VersionSlice, error,
) {
	bundles, err := listBundles(searchCtx, customerzone.NewDir(dir))
	if err != nil {
		return nil, err
	}

	versions := make(version.VersionSlice, len(bundles))
	for i, found := range bundles {
		versions[i] = found.Version
	}

	return versions, nil
}

// SearchVersionsLocal outputs available versions of program found locally.
func SearchVersionsLocal(searchCtx SearchCtx) error {
	root, err := searchCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	if searchCtx.LocalRepo == "" {
		return errLocalRepoRequired
	}

	localDir, err := filepath.Abs(searchCtx.LocalRepo)
	if err != nil {
		return fmt.Errorf("cannot resolve local repository directory: %w", err)
	}

	log.Debugf("Using local search directory: %s", localDir)

	prg := searchCtx.Program
	log.Infof("Available local versions of %s:", prg)

	var vers version.VersionSlice

	switch {
	case prg.GitRepo() != "":
		repoPath := filepath.Join(localDir, prg.String())

		vers, err = searchVersionsLocalGit(prg, repoPath)
	case prg.BundleSource() != binary.ProgramUnknown:
		vers, err = searchVersionsLocalBundles(&searchCtx, localDir)
	default:
		return fmt.Errorf("%w%s'", errLocalSearchNotSupportedForProgram, prg)
	}

	if err != nil {
		return fmt.Errorf("failed to search local versions for %s: %w", prg, err)
	}

	if vers.Len() == 0 {
		log.Infof("No local versions found for %s.", prg)

		return nil // It's not an error if nothing is found.
	}

	for _, v := range vers {
		printVersion(root, prg, v, searchCtx.DevBuilds)
	}

	return nil
}
