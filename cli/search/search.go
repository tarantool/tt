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
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// SearchCtx contains information for programs searching.
type SearchCtx struct {
	// Dbg is set if debug builds of tarantool-ee must be included in the result of search.
	Dbg bool
	// Dev is set if dev builds of tarantool-ee must be included in the result of search.
	Dev bool
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
		if program == "tarantool-ee" {
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

	if program == "tarantool" {
		repo = GitRepoTarantool
		if searchCtx.Dev || searchCtx.Dbg {
			log.Warnf("--dbg and --dev options can be used only for" +
				" tarantool-ee packages searching.")
		}
	} else if program == "tt" {
		repo = GitRepoTT
		if searchCtx.Dev || searchCtx.Dbg {
			log.Warnf("--dbg and --dev options can be used only for" +
				" tarantool-ee packages searching.")
		}
	} else if program == "tarantool-ee" {
		// Do nothing. Needs for bypass arguments check.
	} else {
		return fmt.Errorf("search supports only tarantool/tarantool-ee/tt")
	}

	var err error
	log.Infof("Available versions of " + program + ":")
	if program == "tarantool-ee" {
		bundles, err := FetchBundlesInfo(searchCtx, cliOpts)
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

	if program == "tarantool" {
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
	} else if program == "tt" {
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
	} else if program == "tarantool-ee" {
		files := []string{}
		for _, v := range localFiles {
			if strings.Contains(v.Name(), "tarantool-enterprise-bundle") && !v.IsDir() {
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

// compileVersionRegexp compiles a regular expression for SDK bundle names.
func compileVersionRegexp() ([]*regexp.Regexp, error) {
	matchReNew := ""
	matchReOld := ""

	versionRegexpList := make([]*regexp.Regexp, 2)

	arch, err := util.GetArch()
	if err != nil {
		return nil, err
	}

	osType, err := util.GetOs()
	if err != nil {
		return nil, err
	}

	switch osType {
	case util.OsLinux:
		// Regexp for bundles from the new layout.
		matchReNew = "((?P<prefix>.+/)(?P<tarball>tarantool-enterprise-sdk-" +
			"(?P<version>.*r[0-9]{3})?(?:.linux.)?" +
			arch + "\\.tar\\.gz))"

		// Regexp for bundles from the old layout.
		if arch == "x86_64" {
			matchReOld = "((?P<prefix>.+/)(?P<tarball>tarantool-enterprise-bundle-" +
				"(?P<version>.*-g[a-f0-9]+-r[0-9]{3}(?:-nogc64)?)(?:-linux-x86_64)?\\.tar\\.gz))"
		} else {
			matchReOld = "((?P<prefix>.+/)(?P<tarball>tarantool-enterprise-bundle-" +
				"(?P<version>.*-g[a-f0-9]+-r[0-9]{3}(?:-nogc64)?)(?:-linux-" + arch +
				")\\.tar\\.gz))"
		}
	case util.OsMacos:
		matchReNew = "((?P<prefix>.+/)(?P<tarball>tarantool-enterprise-sdk-" +
			"(?P<version>.*r[0-9]{3})?(?:.macos.)?" +
			arch + "\\.tar\\.gz))"

		if arch == "x86_64" {
			matchReOld = "((?P<prefix>.+/)(?P<tarball>tarantool-enterprise-bundle-" +
				"(?P<version>.*-g[a-f0-9]+-r[0-9]{3})-macos(?:x-x86_64)?\\.tar\\.gz))"
		} else {
			matchReOld = "((?P<prefix>.+/)(?P<tarball>tarantool-enterprise-bundle-" +
				"(?P<version>.*-g[a-f0-9]+-r[0-9]{3})(?:-macosx-" + arch + ")\\.tar\\.gz))"
		}
	}

	reNew := regexp.MustCompile(matchReNew)
	reOld := regexp.MustCompile(matchReOld)

	versionRegexpList[0] = reNew
	versionRegexpList[1] = reOld

	return versionRegexpList, nil
}

// getBundles collects a list of information about all available tarantool-ee
// bundles for the host architecture.
func getBundles(rawBundleInfoList []string) (BundleInfoSlice, error) {
	bundles := BundleInfoSlice{}

	regexpList, err := compileVersionRegexp()
	if err != nil {
		return nil, err
	}

	parsedBundleInfo := make([]map[string]string, 0)
	for _, rawBundleInfo := range rawBundleInfoList {
		newStrategyMatch := util.FindNamedMatches(regexpList[0], rawBundleInfo)
		oldStrategyMatch := util.FindNamedMatches(regexpList[1], rawBundleInfo)
		if newStrategyMatch["version"] != "" {
			parsedBundleInfo = append(parsedBundleInfo, newStrategyMatch)
			continue
		}
		if oldStrategyMatch["version"] != "" {
			parsedBundleInfo = append(parsedBundleInfo, oldStrategyMatch)
		}
	}

	if len(parsedBundleInfo) == 0 {
		return nil, fmt.Errorf("no packages for this OS")
	}

	for _, bundleInfo := range parsedBundleInfo {
		ver, err := version.Parse(bundleInfo["version"])
		if err != nil {
			return nil, err
		}
		eeVersion := BundleInfo{Version: ver}
		eeVersion.Version.Tarball = bundleInfo["tarball"]
		eeVersion.Prefix = bundleInfo["prefix"]
		bundles = append(bundles, eeVersion)
	}

	sort.Sort(bundles)

	return bundles, nil
}

// FetchBundlesInfoLocal returns slice of information about all tarantool-ee
// bundles available locally. The result will be sorted in ascending order.
func FetchBundlesInfoLocal(files []string) ([]BundleInfo, error) {
	versions := BundleInfoSlice{}

	regexpList, err := compileVersionRegexp()
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		parsedData := regexpList[0].FindStringSubmatch(file)
		parsedData = append(parsedData, regexpList[1].FindStringSubmatch(file)...)
		if len(parsedData) == 0 {
			continue
		}

		version, err := version.Parse(parsedData[4])
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
	error) {
	credentials, err := install_ee.GetCreds(cliOpts)
	if err != nil {
		return nil, err
	}

	bundleReferences, err := collectBundleReferences(&searchCtx, install_ee.EESource, credentials)
	if err != nil {
		return nil, err
	}

	bundles, err := getBundles(bundleReferences)
	if err != nil {
		return nil, err
	}

	return bundles, nil
}

// GetTarantoolBundleInfo returns the available EE SDK bundle for user's OS,
// corresponding to the passed expected version argument. If the passed expected version
// is an empty string, it will return the latest release version.
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
		if expectedVersion == "" {
			searchCtx := SearchCtx{Dbg: false, Dev: false}
			bundles, err = FetchBundlesInfo(searchCtx, cliOpts)
			if err != nil {
				return BundleInfo{}, err
			}
			if bundles.Len() == 0 {
				return BundleInfo{}, fmt.Errorf("no version found")
			}
			return bundles[bundles.Len()-1], nil
		} else {
			searchContexts := []SearchCtx{
				{Dbg: false, Dev: false},
				{Dbg: false, Dev: true},
				{Dbg: true, Dev: true},
			}
			for _, searchCtx := range searchContexts {
				bundles, err = FetchBundlesInfo(searchCtx, cliOpts)
				if err != nil {
					return BundleInfo{}, err
				}
				for _, bundle := range bundles {
					if bundle.Version.Str == expectedVersion {
						return bundle, nil
					}
				}
			}
		}
	}

	return BundleInfo{}, fmt.Errorf("%s version of tarantool-ee doesn't exist", expectedVersion)
}
