package search

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// searchVersionsLocalGit handles searching versions from a local Git repository clone.
// It returns a slice of versions found.
func searchVersionsLocalGit(program Program, repoPath string) (
	version.VersionSlice, error,
) {
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		// It's not an error if the local repo doesn't exist.
		log.Debugf("Local repository for %s not found at %s", program, repoPath)
		return nil, nil
	}

	versions, err := GetVersionsFromGitLocal(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from local repo %s: %w", repoPath, err)
	}

	return append(versions, version.Version{Str: "master"}), nil
}

// searchVersionsLocalSDK handles searching versions from locally available SDK bundle files.
// It returns a slice of versions found.
func searchVersionsLocalSDK(program Program, dir string) (
	version.VersionSlice, error,
) {
	bundles, err := FindLocalBundles(program, os.DirFS(dir))
	if err != nil {
		return nil, err
	}

	versions := make(version.VersionSlice, bundles.Len())
	for i, bundle := range bundles {
		versions[i] = bundle.Version
	}

	return versions, nil
}

// fetchBundlesInfoLocal returns slice of information about all tarantool-ee or tcm
// bundles available locally. The result will be sorted in ascending order.
func fetchBundlesInfoLocal(files []string, program Program) (BundleInfoSlice, error) {
	re, err := compileVersionRegexp(program)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regex for %s: %w", program, err)
	}

	versions := make(BundleInfoSlice, 0, len(files))
	for _, file := range files {
		parsedData := util.FindNamedMatches(re, file) // Assumes util package is imported
		if len(parsedData) == 0 {
			continue
		}

		versionStr := parsedData["version"]
		ver, err := version.Parse(versionStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse version from file %s: %w", file, err)
		}

		ver.Tarball = file
		versions = append(versions, BundleInfo{Version: ver})
	}

	sort.Sort(versions)
	return versions, nil
}

// FindLocalBundles finds and parses local SDK bundle files for a given program.
func FindLocalBundles(program Program, fsys fs.FS) (BundleInfoSlice, error) {
	localFiles, err := fs.ReadDir(fsys, ".")
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("Directory not found, cannot search for local SDK files")
			// The directory doesn't exist, it's not an error for searching.
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	files := []string{}
	var prefix string
	switch program {
	case ProgramEe:
		prefix = "tarantool-enterprise-sdk-"
	case ProgramTcm:
		prefix = "tcm-"
	default:
		return nil, fmt.Errorf("local SDK file search not supported for %q", program)
	}

	for _, v := range localFiles {
		if strings.Contains(v.Name(), prefix) && !v.IsDir() {
			files = append(files, v.Name())
		}
	}

	if len(files) == 0 {
		log.Debugf("No local SDK files found for %q", program)
		return nil, nil
	}

	bundles, err := fetchBundlesInfoLocal(files, program)
	if err != nil {
		return nil, err
	}
	return bundles, nil
}

// getBaseDirectory determines the base directory for local search.
func getBaseDirectory(cfgPath string, repo *config.RepoOpts) string {
	localDir := ""
	if repo != nil && repo.Install != "" {
		localDir = repo.Install
	} else {
		configDir := filepath.Dir(cfgPath)
		localDir = filepath.Join(configDir, "distfiles")
	}

	log.Debugf("Using local search directory: %s", localDir)
	return localDir
}

// SearchVersionsLocal outputs available versions of program found locally.
func SearchVersionsLocal(searchCtx SearchCtx, cliOpts *config.CliOpts, cfgPath string) error {
	prg := searchCtx.Program
	log.Infof("Available local versions of %s:", prg)

	localDir := getBaseDirectory(cfgPath, cliOpts.Repo)

	var vers version.VersionSlice
	var err error

	switch prg {
	case ProgramCe:
		repoPath := filepath.Join(localDir, "tarantool")
		vers, err = searchVersionsLocalGit(prg, repoPath)
	case ProgramTt:
		repoPath := filepath.Join(localDir, "tt")
		vers, err = searchVersionsLocalGit(prg, repoPath)
	case ProgramEe, ProgramTcm:
		vers, err = searchVersionsLocalSDK(prg, localDir)
	default:
		return fmt.Errorf("local search not supported for program '%s'", prg)
	}

	if err != nil {
		return fmt.Errorf("failed to search local versions for %s: %w", prg, err)
	}

	if vers.Len() == 0 {
		log.Infof("No local versions found for %s.", prg)
		return nil // It's not an error if nothing is found.
	}

	for _, v := range vers {
		printVersion(cliOpts.Env.BinDir, prg, v.Str)
	}

	return nil
}
