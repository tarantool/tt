package search

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

type SearchFlags int64

const (
	SearchRelease SearchFlags = iota
	SearchDebug
	SearchAll
)

// SearchCtx contains information for programs searching.
type SearchCtx struct {
	// Filter out which builds of tarantool-ee must be included in the result of search.
	Filter SearchFlags
	// What package to look for.
	Package string
	// Release version to look for.
	ReleaseVersion string
}

const (
	GitRepoTarantool = "https://github.com/tarantool/tarantool.git"
	GitRepoTT        = "https://github.com/tarantool/tt.git"
)

// isMasked function checks that the given version of tarantool is masked.
func isMasked(version version.Version) bool {
	// Mask all versions below 1.10: deprecated.
	if version.Major == 1 && version.Minor < 10 {
		return true
	}

	// Mask all versions below 1.10.11: static build is not supported.
	if version.Major == 1 && version.Minor == 10 && version.Patch < 11 {
		return true
	}

	// Mask all versions below 2.7: static build is not supported.
	if version.Major == 2 && version.Minor < 7 {
		return true
	}

	// Mask 2.10.1 version: https://github.com/orgs/tarantool/discussions/7646.
	if version.Major == 2 && version.Minor == 10 && version.Patch == 1 {
		return true
	}

	// Mask all 2.X.0 below 2.10.0: technical tags.
	if version.Major == 2 && version.Minor < 10 && version.Patch == 0 {
		return true
	}

	return false
}

// GetVersionsFromGitRemote returns sorted versions list from specified remote git repo.
func GetVersionsFromGitRemote(repo string) ([]version.Version, error) {
	versions := []version.Version{}

	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("'git' is required for 'tt search' to work")
	}

	output, err := exec.Command("git", "ls-remote", "--tags", "--refs", repo).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from %s: %s", repo, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	// No tags found.
	if len(lines) == 1 && lines[0] == "" {
		return versions, nil
	}

	for _, line := range lines {
		slashIdx := strings.LastIndex(line, "/")
		if slashIdx == -1 {
			return nil, fmt.Errorf("unexpected Data from %s", repo)
		} else {
			slashIdx += 1
		}
		ver := line[slashIdx:]
		version, err := version.Parse(ver)
		if err != nil {
			continue
		}
		if isMasked(version) && repo == GitRepoTarantool {
			continue
		}
		versions = append(versions, version)
	}

	sort.Stable(version.VersionSlice(versions))

	return versions, nil
}

// GetVersionsFromGitLocal returns sorted versions list from specified local git repo.
func GetVersionsFromGitLocal(repo string) ([]version.Version, error) {
	versions := []version.Version{}

	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("'git' is required for 'tt search' to work")
	}

	output, err := exec.Command("git", "-C", repo, "tag").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from %s: %s", repo, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	// No tags found.
	if len(lines) == 1 && lines[0] == "" {
		return versions, nil
	}

	for _, line := range lines {
		version, err := version.Parse(line)
		if err != nil {
			continue
		}
		if isMasked(version) && strings.Contains(repo, "tarantool") {
			continue
		}
		versions = append(versions, version)
	}

	sort.Stable(version.VersionSlice(versions))

	return versions, nil
}

// printVersion prints the version and labels:
// * if the package is installed: [installed]
// * if the package is installed and in use: [active]
func printVersion(bindir string, program string, versionStr string) {
	if _, err := os.Stat(filepath.Join(bindir,
		program+version.FsSeparator+versionStr)); err == nil {
		target := ""
		if program == ProgramEe {
			target, _ = util.ResolveSymlink(filepath.Join(bindir, "tarantool"))
		} else {
			target, _ = util.ResolveSymlink(filepath.Join(bindir, program))
		}

		if path.Base(target) == program+version.FsSeparator+versionStr {
			fmt.Printf("%s [active]\n", versionStr)
		} else {
			fmt.Printf("%s [installed]\n", versionStr)
		}
	} else {
		fmt.Println(versionStr)
	}
}

// SearchVersions outputs available versions of program.
func SearchVersions(cmdCtx *cmdcontext.CmdCtx, searchCtx SearchCtx,
	cliOpts *config.CliOpts, program string) error {
	var repo string
	versions := []version.Version{}

	if program == ProgramCe {
		repo = GitRepoTarantool
		if searchCtx.Filter == SearchDebug || (len(searchCtx.ReleaseVersion) > 0) {
			log.Warnf("--debug and --version options can only be used to" +
				" search for tarantool-ee packages.")
		}
	} else if program == ProgramTt {
		repo = GitRepoTT
		if searchCtx.Filter == SearchDebug || (len(searchCtx.ReleaseVersion) > 0) {
			log.Warnf("--debug and --version options can only be used to" +
				" search for tarantool-ee packages.")
		}
	} else if program == ProgramEe {
		// Do nothing. Needs for bypass arguments check.
	} else {
		return fmt.Errorf("search supports only tarantool/tarantool-ee/tt")
	}

	var err error
	log.Infof("Available versions of " + program + ":")
	if program == ProgramEe {
		searchCtx.Package = "enterprise"
		bundles, _, err := FetchBundlesInfo(searchCtx, cliOpts)
		if err != nil {
			log.Fatalf(err.Error())
		}

		for _, bundle := range bundles {
			printVersion(cliOpts.App.BinDir, program, bundle.Version.Str)
		}
		return nil
	}

	versions, err = GetVersionsFromGitRemote(repo)
	if err != nil {
		log.Fatalf(err.Error())
	}

	for _, version := range versions {
		printVersion(cliOpts.App.BinDir, program, version.Str)
	}

	printVersion(cliOpts.App.BinDir, program, "master")

	return err
}

// RunCommandAndGetOutputInDir returns output of command.
func RunCommandAndGetOutputInDir(program string, dir string, args ...string) (string, error) {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// SearchVersionsLocal outputs available versions of program from distfiles directory.
func SearchVersionsLocal(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, program string) error {
	var err error
	if cliOpts.Repo == nil {
		cliOpts.Repo = &config.RepoOpts{Install: "", Rocks: ""}
	}
	localDir := cliOpts.Repo.Install
	if localDir == "" {
		configDir := filepath.Dir(cmdCtx.Cli.ConfigPath)
		localDir = filepath.Join(configDir, "distfiles")
	}

	localFiles, err := os.ReadDir(localDir)
	if err != nil {
		return err
	}

	if program == ProgramCe {
		if _, err = os.Stat(localDir + "/tarantool"); !os.IsNotExist(err) {
			log.Infof("Available versions of " + program + ":")
			versions, err := GetVersionsFromGitLocal(localDir + "/tarantool")
			if err != nil {
				log.Fatalf(err.Error())
			}

			for _, version := range versions {
				printVersion(cliOpts.App.BinDir, program, version.Str)
			}
			printVersion(cliOpts.App.BinDir, program, "master")
		}
	} else if program == ProgramTt {
		if _, err = os.Stat(localDir + "/tt"); !os.IsNotExist(err) {
			log.Infof("Available versions of " + program + ":")
			versions, err := GetVersionsFromGitLocal(localDir + "/tt")
			if err != nil {
				log.Fatalf(err.Error())
			}

			for _, version := range versions {
				printVersion(cliOpts.App.BinDir, program, version.Str)
			}
			printVersion(cliOpts.App.BinDir, program, "master")
		}
	} else if program == ProgramEe {
		files := []string{}
		for _, v := range localFiles {
			if strings.Contains(v.Name(), "tarantool-enterprise-sdk") && !v.IsDir() {
				files = append(files, v.Name())
			}
		}

		log.Infof("Available versions of " + program + ":")
		bundles, err := FetchBundlesInfoLocal(files)
		if err != nil {
			log.Fatalf(err.Error())
		}

		for _, bundle := range bundles {
			printVersion(cliOpts.App.BinDir, program, bundle.Version.Str)
		}
	} else {
		return fmt.Errorf("search supports only tarantool/tarantool-ee/tt")
	}

	return err
}

// compileVersionRegexp compiles a regular expression for cutting version from SDK bundle names.
func compileVersionRegexp() (*regexp.Regexp, error) {
	matchRe := "^(?P<tarball>tarantool-enterprise-sdk-(?P<version>.*r[0-9]{3}).*\\.tar\\.gz)$"

	re := regexp.MustCompile(matchRe)

	return re, nil
}

// getBundles collects a list of information about all available tarantool-ee
// bundles from tarantool.io api reply.
func getBundles(rawBundleInfoList map[string][]string, flags SearchFlags) (BundleInfoSlice,
	error) {
	bundles := BundleInfoSlice{}

	re, err := compileVersionRegexp()
	if err != nil {
		return nil, err
	}

	for release, pkgs := range rawBundleInfoList {
		for _, pkg := range pkgs {
			parsedData := util.FindNamedMatches(re, pkg)
			if len(parsedData) == 0 {
				continue
			}

			version, err := version.Parse(parsedData["version"])
			if err != nil {
				return nil, err
			}

			version.Tarball = pkg
			eeVer := BundleInfo{
				Version: version,
				Package: "enterprise",
				Release: release,
			}

			switch flags {
			case SearchRelease:
				if strings.Contains(pkg, "-debug-") {
					continue
				}
			case SearchDebug:
				if !strings.Contains(pkg, "-debug-") {
					continue
				}
			}

			bundles = append(bundles, eeVer)
		}
	}

	if len(bundles) == 0 {
		return nil, fmt.Errorf("no packages found for this OS or release version")
	}

	sort.Sort(bundles)

	return bundles, nil
}

// FetchBundlesInfoLocal returns slice of information about all tarantool-ee
// bundles available locally. The result will be sorted in ascending order.
func FetchBundlesInfoLocal(files []string) ([]BundleInfo, error) {
	versions := BundleInfoSlice{}

	re, err := compileVersionRegexp()
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		parsedData := util.FindNamedMatches(re, file)
		if len(parsedData) == 0 {
			continue
		}

		version, err := version.Parse(parsedData["version"])
		if err != nil {
			return nil, err
		}

		version.Tarball = file
		eeVer := BundleInfo{Version: version}
		versions = append(versions, eeVer)
	}

	sort.Sort(versions)

	return versions, nil
}

// FetchBundlesInfo returns slice of information about all available tarantool-ee bundles.
// The result will be sorted in ascending order.
func FetchBundlesInfo(searchCtx SearchCtx, cliOpts *config.CliOpts) ([]BundleInfo,
	string, error) {
	bundleReferences, token, err := tntIoGetPkgVersions(cliOpts, searchCtx)
	if err != nil {
		return nil, "", err
	}

	bundles, err := getBundles(bundleReferences, searchCtx.Filter)
	if err != nil {
		return nil, "", err
	}

	return bundles, token, nil
}

// GetTarantoolBundleInfo returns the available EE SDK bundle for user's OS,
// corresponding to the passed expected version argument.
func GetTarantoolBundleInfo(cliOpts *config.CliOpts, local bool,
	files []string, expectedVersion string) (BundleInfo, error) {
	bundles := BundleInfoSlice{}
	var err error

	if local {
		bundles, err = FetchBundlesInfoLocal(files)
		if expectedVersion == "" {
			return bundles[bundles.Len()-1], nil
		}
		for _, bundle := range bundles {
			if bundle.Version.Str == expectedVersion {
				return bundle, nil
			}
		}
	} else {
		var token string
		searchCtx := SearchCtx{
			Filter:  SearchAll,
			Package: "enterprise",
		}
		bundles, token, err = FetchBundlesInfo(searchCtx, cliOpts)
		if err != nil {
			return BundleInfo{}, err
		}
		for _, bundle := range bundles {
			if bundle.Version.Str == expectedVersion {
				bundle.Token = token
				return bundle, nil
			}
		}
	}

	return BundleInfo{}, fmt.Errorf("%s version of tarantool-ee doesn't exist", expectedVersion)
}
