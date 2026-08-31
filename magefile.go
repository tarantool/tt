//go:build mage
// +build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/apex/log"
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// spell-checker:ignore trimpath extldflags asmflags covdata GOEXE TTEXE GOCOVERDIR

const (
	buildTypeEnv  = "TT_CLI_BUILD_SSL"
	goPackageName = "github.com/tarantool/tt/cli"

	asmflags = "all=-trimpath=${PWD}"
	gcflags  = "all=-trimpath=${PWD}"

	packagePath = "./cli"

	defaultLinuxConfigPath  = "/etc/tarantool"
	defaultDarwinConfigPath = "/usr/local/etc/tarantool"

	// Two golangci-lint configs live in the tree, one per major version of the
	// linter, and they are mutually unreadable: v2 refuses a config with no
	// `version` key and v1 refuses one that has it. lintConfigV1 is the
	// project-wide ruleset; lintConfigV2 is the strict `default: all` one, whose
	// own path-except rule scopes it to the packages already cleaned up for it.
	lintConfigV1 = "golangci-lint.yml"
	lintConfigV2 = ".golangci.yml"

	// Env vars naming an alternative linter binary, one per major version. There
	// are two because the two targets need two different majors, so a single
	// override could not serve both.
	lintExeEnvV1 = "GOLANGCI_LINT_V1"
	lintExeEnvV2 = "GOLANGCI_LINT_V2"
)

var (
	ldflags = []string{
		"-X ${PACKAGE}/version.gitTag=${GIT_TAG}",
		"-X ${PACKAGE}/version.gitCommit=${GIT_COMMIT}",
		"-X ${PACKAGE}/version.gitCommitSinceTag=${GIT_COMMIT_SINCE_TAG}",
		"-X ${PACKAGE}/version.versionLabel=${VERSION_LABEL}",
		"-X ${PACKAGE}/configure.defaultConfigPath=${CONFIG_PATH}",
	}
	staticLdflags = []string{
		"-linkmode=external", "-extldflags", "-static",
	}
	goExecutableName     = "go"
	pythonExecutableName = "python3"
	ttExecutableName     = "tt"

	Aliases = map[string]any{
		"build":    Build.Release,
		"unit":     Unit.Default,
		"unitfull": Unit.Full,
	}

	modules = []string{
		"lib/integrity",
		"lib/cluster",
	}

	// lintExeCandidates lists the binaries tried, in order, when the matching
	// env var is unset. The version-suffixed names are what a side-by-side
	// install provides; the bare name is the fallback for a single install, and
	// it is checked rather than assumed - which major it happens to be is the
	// whole question.
	lintExeCandidates = map[int][]string{
		1: {"golangci-lint@v1", "golangci-lint"},
		2: {"golangci-lint@v2", "golangci-lint"},
	}

	// lintVersionRe pulls the version out of `golangci-lint version`, which
	// prints e.g. "golangci-lint has version 2.11.4 built with go1.26.1 from ...".
	lintVersionRe = regexp.MustCompile(`\bversion v?(\d+)\.(\d+)\.(\d+)\b`)
)

type BuildType string

const (
	BuildTypeDefault BuildType = ""
	BuildTypeNoCgo   BuildType = "no"
	BuildTypeShared  BuildType = "shared"
	BuildTypeStatic  BuildType = "static"
)

func init() {
	var err error

	if specifiedGoExe := os.Getenv("GOEXE"); specifiedGoExe != "" {
		goExecutableName = specifiedGoExe
	}

	if specifiedTTExe := os.Getenv("TTEXE"); specifiedTTExe != "" {
		ttExecutableName = specifiedTTExe
	} else {
		if ttExecutableName, err = filepath.Abs(ttExecutableName); err != nil {
			panic(err)
		}
	}
	// We want to use Go 1.11 modules even if the source lives inside GOPATH.
	// The default is "auto".
	os.Setenv("GO111MODULE", "on")
}

type optsUpdater func([]string) ([]string, error)

// appendFlags appends flags passed in args.
func appendFlags(flags ...string) optsUpdater {
	return func(args []string) ([]string, error) {
		return append(args, flags...), nil
	}
}

// appendLdFlags appends linker flags.
func appendLdFlags(flags ...string) optsUpdater {
	return func(args []string) ([]string, error) {
		buildLdflags := make([]string, len(ldflags))
		copy(buildLdflags, ldflags)
		buildLdflags = append(buildLdflags, flags...)

		buildType := os.Getenv(buildTypeEnv)
		if BuildType(buildType) == BuildTypeStatic && runtime.GOOS != "darwin" {
			buildLdflags = append(buildLdflags, staticLdflags...)
		}
		return append(append(args, "-ldflags"), strings.Join(buildLdflags, " ")), nil
	}
}

// appendTags appends tags.
func appendTags(args []string) ([]string, error) {
	tags := []string{"netgo", "osusergo", "go_tarantool_msgpack_v5"}

	buildType := os.Getenv(buildTypeEnv)
	switch BuildType(buildType) {
	case BuildTypeDefault:
		fallthrough
	case BuildTypeNoCgo:
		tags = append(tags, "go_tarantool_ssl_disable", "tt_ssl_disable")
	case BuildTypeStatic:
		tags = append(tags, "openssl_static")
	case BuildTypeShared:
	default:
		return []string{}, fmt.Errorf("unsupported build type: %s, supported: "+
			"%s, %s, %s",
			buildType, BuildTypeNoCgo, BuildTypeStatic, BuildTypeShared)
	}
	return append(append(args, "-tags"), strings.Join(tags, ",")), nil
}

// Building tt executable. Supported environment variables:
// TT_CLI_BUILD_SSL=(no|static|shared).
func buildTt(argUpdaters ...optsUpdater) error {
	args := []string{"build", "-o", ttExecutableName}
	var err error
	for _, updateArguments := range argUpdaters {
		if args, err = updateArguments(args); err != nil {
			return err
		}
	}
	args = append(args,
		"-asmflags", asmflags,
		"-gcflags", gcflags,
		packagePath)
	err = sh.RunWith(getBuildEnvironment(), goExecutableName, args...)
	if err != nil {
		return fmt.Errorf("failed to build tt executable: %s", err)
	}

	return nil
}

type Build mg.Namespace

// Building release tt executable without debug info.
func (Build) Release() error {
	fmt.Println("Building release tt...")

	return buildTt(appendTags, appendLdFlags("-s", "-w"))
}

// Building debug tt executable.
func (Build) Debug() error {
	fmt.Println("Building debug tt...")

	return buildTt(appendTags, appendLdFlags())
}

// Building tt executable with coverage.
func (Build) Coverage() error {
	fmt.Println("Building release tt with coverage...")

	err := buildTt(appendFlags("-cover"), appendTags, appendLdFlags("-s", "-w"))
	if err != nil {
		return err
	}
	fmt.Println(`Set coverage data destination directory (must exist) and run tt:
	GOCOVERDIR=./<coverage_dest_dir> tt <opts>`)
	return nil
}

// Run license checker.
func CheckLicenses() error {
	fmt.Println("Running license checker...")

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	if err := sh.RunV(home+"/go/bin/lichen", "--config", ".lichen.yaml", "tt"); err != nil {
		return err
	}

	return nil
}

type Lint mg.Namespace

// Run golang and python linters.
func (Lint) Full() error {
	mg.Deps(Lint.Golang, Lint.Strict, Lint.Python)
	return nil
}

// Run golang linters.
func (Lint) Golang() error {
	linter, err := resolveLinter(1, lintExeEnvV1)
	if err != nil {
		return err
	}

	fmt.Printf("Running %s over %s...\n", linter, lintConfigV1)

	lintDirs := append([]string{"."}, modules...)
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current dir: %w", err)
	}

	for _, dir := range lintDirs {
		os.Chdir(dir)
		if err := sh.RunV(linter, "run",
			fmt.Sprintf("--config=%s/%s", root, lintConfigV1)); err != nil {
			return err
		}
		os.Chdir(root)
	}
	return nil
}

// Run the strict golang ruleset over the packages that have been cleaned up for
// it.
//
// This is a separate target rather than part of Lint.Golang because the two
// rulesets need different linters: .golangci.yml is a v2 config and
// golangci-lint.yml is a v1 one, and neither major can read the other's format.
// The strict config carries its own path-except rule naming the adopted
// packages, so no path arguments are passed here - widening the scope is an
// edit to that rule, not to this target.
func (Lint) Strict() error {
	linter, err := resolveLinter(2, lintExeEnvV2)
	if err != nil {
		return err
	}

	fmt.Printf("Running %s over %s...\n", linter, lintConfigV2)

	// Only the root module: the strict config's scope lives inside cli/, and
	// lib/* are separate modules that have not been adopted.
	return sh.RunV(linter, "run", "--config="+lintConfigV2)
}

// resolveLinter returns the golangci-lint executable to run a config of the
// given major version, having checked that it exists and reports that major.
//
// The check is worth its weight because the failure it replaces is unreadable:
// a v2 binary handed a v1 config dies with `unsupported version of the
// configuration: ""`, and a v1 binary handed a v2 one dies complaining about
// the Go version, neither of which names the actual problem.
//
// envVar names an alternative binary and is honored exactly: a binary named
// there whose major is wrong is an error, never a silent fallback to another
// one, because the caller asked for that binary specifically. With envVar
// unset, the versioned names a side-by-side install provides are tried first
// and the bare name last.
func resolveLinter(major int, envVar string) (string, error) {
	if specified := os.Getenv(envVar); specified != "" {
		found, version, err := linterMajor(specified)
		if err != nil {
			return "", fmt.Errorf("%s=%s: %w", envVar, specified, err)
		}

		if found != major {
			return "", fmt.Errorf(
				"%s=%s is golangci-lint %s, but %s is a v%d config that only a v%d "+
					"linter can read", envVar, specified, version, lintConfigFor(major),
				major, major)
		}

		return specified, nil
	}

	var tried []string

	for _, candidate := range lintExeCandidates[major] {
		tried = append(tried, candidate)

		if _, err := exec.LookPath(candidate); err != nil {
			continue
		}

		found, _, err := linterMajor(candidate)
		if err != nil || found != major {
			continue
		}

		return candidate, nil
	}

	return "", fmt.Errorf(
		"no golangci-lint v%d found for %s (tried: %s); install one or point %s at it",
		major, lintConfigFor(major), strings.Join(tried, ", "), envVar)
}

// linterMajor runs `<exe> version` and reports the major version it prints,
// along with the full version string for diagnostics.
func linterMajor(exe string) (int, string, error) {
	if _, err := exec.LookPath(exe); err != nil {
		return 0, "", fmt.Errorf("not found: %w", err)
	}

	out, err := sh.Output(exe, "version")
	if err != nil {
		return 0, "", fmt.Errorf("running %q version: %w", exe, err)
	}

	match := lintVersionRe.FindStringSubmatch(out)
	if match == nil {
		return 0, "", fmt.Errorf("cannot parse a version out of %q", strings.TrimSpace(out))
	}

	major, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, "", fmt.Errorf("cannot parse major version from %q", match[0])
	}

	return major, match[1] + "." + match[2] + "." + match[3], nil
}

// lintConfigFor names the config a given linter major reads.
func lintConfigFor(major int) string {
	if major == 1 {
		return lintConfigV1
	}

	return lintConfigV2
}

// Run python linters.
func (Lint) Python() error {
	fmt.Println("Running Ruff...")

	if err := sh.RunV(pythonExecutableName, "-m", "ruff", "check", "test"); err != nil {
		return err
	}

	return nil
}

type Unit mg.Namespace

func runUnitTests(flags []string) error {
	testDirs := append([]string{"."}, modules...)
	for _, module := range testDirs {
		args := []string{"test", "-C", module}
		if mg.Verbose() {
			args = append(args, "-v")
		}
		args = append(args, "./...")
		args = append(args, flags...)
		args = append(args, "-count=1")

		err := sh.RunV(goExecutableName, args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// Run unit tests.
func (Unit) Default() error {
	fmt.Println("Running unit tests...")

	return runUnitTests([]string{})
}

// Run unit tests with a Tarantool instance integration.
func (Unit) Full() error {
	fmt.Println("Running full unit tests...")

	return runUnitTests([]string{"-tags", "integration,integration_docker"})
}

// Run unit tests with a Tarantool instance integration, excluding docker tests.
func (Unit) FullSkipDocker() error {
	fmt.Println("Running full unit tests, excluding docker...")

	return runUnitTests([]string{"-tags", "integration"})
}

// Run full unit tests set with code coverage.
func (Unit) Coverage() error {
	fmt.Println("Running full unit tests with code coverage...")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	coverDir := filepath.Join(cwd, "coverage", "unit")
	coverageDirInfo, err := os.Stat(coverDir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(coverDir, 0o750); err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		if !coverageDirInfo.IsDir() {
			return fmt.Errorf("%q is not a directory", coverDir)
		}
	}

	err = runUnitTests([]string{
		"-tags", "integration,integration_docker",
		"-cover",
		"-args", fmt.Sprintf(`-test.gocoverdir=%s`, coverDir),
	})
	if err != nil {
		return err
	}
	relCoverDir, err := filepath.Rel(cwd, coverDir)
	if err != nil {
		relCoverDir = coverDir
	}
	fmt.Printf("Coverage data is saved to %q\n", relCoverDir)
	fmt.Printf(`Example command for analysis:
	go tool covdata func -i %q
`, relCoverDir)

	return nil
}

// Run integration tests, excluding slow tests.
func Integration() error {
	fmt.Println("Running integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "not slow and not slow_ee "+
		"and not notarantool", "test/integration")
}

// Run full set of integration tests.
func IntegrationFull() error {
	fmt.Println("Running all integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "not slow_ee and not notarantool",
		"test/integration")
}

// Run full set of integration tests, excluding docker tests.
func IntegrationFullSkipDocker() error {
	fmt.Println("Running all integration tests, excluding docker...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m",
		"not slow_ee and not notarantool and not docker", "test/integration")
}

// Run only docker tests from the full set.
func IntegrationFullDocker() error {
	fmt.Println("Running docker integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m",
		"not slow_ee and not notarantool and docker", "test/integration")
}

// Run set of ee integration tests.
func IntegrationEE() error {
	fmt.Println("Running all EE integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "test/integration/ee")
}

// Run integration tests without system-wide installed Tarantool.
func IntegrationNoTarantool() error {
	fmt.Println("Running integration tests without Tarantool...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "notarantool",
		"test/integration")
}

// Run code spell checks.
func CodeSpell() error {
	fmt.Println("Running code spell tests...")

	return sh.RunV("codespell", ".") // spell-checker:disable-line
}

// Run all tests together, excluding slow and unit integration tests.
func Test() {
	mg.SerialDeps(Lint.Full, CheckLicenses, Unit.Default, Integration)
}

// Run all tests together.
func TestFull() {
	mg.SerialDeps(Lint.Full, CheckLicenses, Unit.Full, IntegrationFull)
}

// Cleanup directory.
func Clean() {
	fmt.Println("Cleaning directory...")

	os.Remove(ttExecutableName)
}

// Generate generates code as usual `go generate` command. To work properly you
// will need a latest Tarantool executable in PATH.
func Generate() error {
	paths := append([]string{"."}, modules...)
	for _, path := range paths {
		err := sh.RunWith(getBuildEnvironment(), goExecutableName, "-C", path,
			"generate", "./...")
		if err != nil {
			return fmt.Errorf("failed to generate sources for path %q: %w", path, err)
		}
	}
	return nil
}

// getDefaultConfigPath returns the path to the configuration file,
// determining it based on the OS.
func getDefaultConfigPath() string {
	switch runtime.GOOS {
	case "linux":
		return defaultLinuxConfigPath
	case "darwin", "freebsd":
		return defaultDarwinConfigPath
	}

	log.Fatalf("Trying to get default config path file on an unsupported OS")
	return ""
}

// getBuildEnvironment return map with build environment variables.
func getBuildEnvironment() map[string]string {
	var err error

	var currentDir string
	var gitTag string
	var gitTagShort string
	var gitCommit string
	var gitCommitSinceTag string

	if currentDir, err = os.Getwd(); err != nil {
		log.Warnf("Failed to get current directory: %s", err)
	}

	if _, err := exec.LookPath("git"); err == nil {
		gitTag, _ = sh.Output("git", "describe", "--tags")
		gitTagShort, _ = sh.Output("git", "describe", "--tags", "--abbrev=0")
		gitCommit, _ = sh.Output("git", "rev-parse", "--short", "HEAD")
		gitCommitSinceTag, _ = sh.Output("git", "rev-list", gitTagShort+"..", "--count")
	}

	return map[string]string{
		"PACKAGE":              goPackageName,
		"GIT_TAG":              gitTag,
		"GIT_COMMIT":           gitCommit,
		"GIT_COMMIT_SINCE_TAG": gitCommitSinceTag,
		"VERSION_LABEL":        os.Getenv("VERSION_LABEL"),
		"PWD":                  currentDir,
		"CONFIG_PATH":          getDefaultConfigPath(),
		"CGO_ENABLED":          "1",
	}
}
