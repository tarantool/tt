package search

import (
	"fmt"
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
func searchVersionsLocalGit(program, repoPath string) (
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
func searchVersionsLocalSDK(program, localDir string) (
	version.VersionSlice, error,
) {
	localFiles, err := os.ReadDir(localDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("Local directory %s not found, cannot search for local SDK files.", localDir)
			// The directory doesn't exist, it's not an error for searching.
			return nil, nil
		}
		// Other errors (e.g., permissions) are actual errors.
		return nil, fmt.Errorf("failed to read local directory %s: %w", localDir, err)
	}

	files := []string{}
	var prefix string
	switch program {
	case ProgramEe:
		prefix = "tarantool-enterprise-sdk-"
	case ProgramTcm:
		prefix = "tcm-"
	default:
		// Should not happen if called correctly, but good practice to handle.
		return nil, fmt.Errorf("local SDK file search not supported for %s", program)
	}

	for _, v := range localFiles {
		if strings.Contains(v.Name(), prefix) && !v.IsDir() {
			files = append(files, v.Name())
		}
	}

	if len(files) == 0 {
		log.Debugf("No local SDK files found for %s in %s", program, localDir)
		return nil, nil
	}

	bundles, err := fetchBundlesInfoLocal(files, program)
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
// Needs 'program' parameter to select correct regex.
func fetchBundlesInfoLocal(files []string, program string) (BundleInfoSlice, error) {
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

// getBaseDirectory determines the base directory for local search (usually 'distfiles').
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
	prg := searchCtx.ProgramName
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

func getSdkBundleInfoLocal(files []string, expectedVersion string) (BundleInfo, error) {
	bundles, err := fetchBundlesInfoLocal(files, ProgramEe)
	if err != nil {
		return BundleInfo{}, err
	}
	if expectedVersion == "" {
		return bundles[bundles.Len()-1], nil
	}
	for _, bundle := range bundles {
		if bundle.Version.Str == expectedVersion {
			return bundle, nil
		}
	}
	return BundleInfo{}, fmt.Errorf("%s version doesn't exist locally", expectedVersion)
}
