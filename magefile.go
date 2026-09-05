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

	lintConfig  = ".golangci.yml"
	lintVersion = "2.13.2"
	lintExeEnv  = "GOLANGCI_LINT"
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
	lintModules = []string{
		".",
		"lib/cluster",
		"lib/connect",
		"lib/dial",
		"lib/integrity",
		"test/integration/aeon/server",
	}

	// lintVersionRe pulls the version out of `golangci-lint version`, which
	// prints e.g. "golangci-lint has version 2.13.2 built with go1.26.8 from ...".
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
		buildLdflags := append([]string(nil), ldflags...)
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
	args := make([]string, 0, 8)
	args = append(args, "build", "-o", ttExecutableName)
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

// Release builds the release tt executable without debug info.
func (Build) Release() error {
	fmt.Println("Building release tt...")

	return buildTt(appendTags, appendLdFlags("-s", "-w"))
}

// Debug builds the debug tt executable.
func (Build) Debug() error {
	fmt.Println("Building debug tt...")

	return buildTt(appendTags, appendLdFlags())
}

// Coverage builds the tt executable with coverage.
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

// CheckLicenses runs the license checker.
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

// Full runs golang and python linters.
func (Lint) Full() error {
	mg.Deps(Lint.Golang, Lint.Python)
	return nil
}

// Golang runs golang linters.
func (Lint) Golang() error {
	linter, err := resolveLinter()
	if err != nil {
		return err
	}

	fmt.Printf("Running %s over %s...\n", linter, lintConfig)

	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current dir: %w", err)
	}

	for _, dir := range lintModules {
		err := os.Chdir(filepath.Join(root, dir))
		if err != nil {
			return fmt.Errorf("failed to enter lint directory %q: %w", dir, err)
		}

		err = sh.RunV(linter, "run",
			fmt.Sprintf("--config=%s/%s", root, lintConfig))
		if err != nil {
			_ = os.Chdir(root)
			return err
		}

		err = os.Chdir(root)
		if err != nil {
			return fmt.Errorf("failed to restore working directory: %w", err)
		}
	}

	return nil
}

// resolveLinter returns the configured golangci-lint executable after checking
// that it is the project-pinned version.
func resolveLinter() (string, error) {
	linter := os.Getenv(lintExeEnv)
	if linter == "" {
		linter = "golangci-lint"
	}

	version, err := linterVersionOf(linter)
	if err != nil {
		return "", fmt.Errorf("%s=%s: %w", lintExeEnv, linter, err)
	}

	if version != lintVersion {
		return "", fmt.Errorf(
			"%s is golangci-lint %s, want the project-pinned version %s",
			linter, version, lintVersion)
	}

	return linter, nil
}

// linterVersionOf runs `<exe> version` and reports its semantic version.
func linterVersionOf(exe string) (string, error) {
	if _, err := exec.LookPath(exe); err != nil {
		return "", fmt.Errorf("not found: %w", err)
	}

	out, err := sh.Output(exe, "version")
	if err != nil {
		return "", fmt.Errorf("running %q version: %w", exe, err)
	}

	match := lintVersionRe.FindStringSubmatch(out)
	if match == nil {
		return "", fmt.Errorf("cannot parse a version out of %q", strings.TrimSpace(out))
	}

	return match[1] + "." + match[2] + "." + match[3], nil
}

// Python runs python linters.
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

// Default runs unit tests.
func (Unit) Default() error {
	fmt.Println("Running unit tests...")

	return runUnitTests([]string{})
}

// Full runs unit tests with Tarantool instance integration.
func (Unit) Full() error {
	fmt.Println("Running full unit tests...")

	return runUnitTests([]string{"-tags", "integration,integration_docker"})
}

// FullSkipDocker runs unit tests with Tarantool instance integration, excluding docker tests.
func (Unit) FullSkipDocker() error {
	fmt.Println("Running full unit tests, excluding docker...")

	return runUnitTests([]string{"-tags", "integration"})
}

// Coverage runs the full unit test set with code coverage.
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

// Integration runs integration tests, excluding slow tests.
func Integration() error {
	fmt.Println("Running integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "not slow and not slow_ee "+
		"and not notarantool", "test/integration")
}

// IntegrationFull runs the full set of integration tests.
func IntegrationFull() error {
	fmt.Println("Running all integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "not slow_ee and not notarantool",
		"test/integration")
}

// IntegrationFullSkipDocker runs the full set of integration tests, excluding docker tests.
func IntegrationFullSkipDocker() error {
	fmt.Println("Running all integration tests, excluding docker...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m",
		"not slow_ee and not notarantool and not docker", "test/integration")
}

// IntegrationFullDocker runs only docker tests from the full set.
func IntegrationFullDocker() error {
	fmt.Println("Running docker integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m",
		"not slow_ee and not notarantool and docker", "test/integration")
}

// IntegrationEE runs the set of EE integration tests.
func IntegrationEE() error {
	fmt.Println("Running all EE integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "test/integration/ee")
}

// IntegrationNoTarantool runs integration tests without a system-wide Tarantool installation.
func IntegrationNoTarantool() error {
	fmt.Println("Running integration tests without Tarantool...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "notarantool",
		"test/integration")
}

// CodeSpell runs code spell checks.
func CodeSpell() error {
	fmt.Println("Running code spell tests...")

	return sh.RunV("codespell", ".") // spell-checker:disable-line
}

// Test runs all tests together, excluding slow and unit integration tests.
func Test() {
	mg.SerialDeps(Lint.Full, CheckLicenses, Unit.Default, Integration)
}

// TestFull runs all tests together.
func TestFull() {
	mg.SerialDeps(Lint.Full, CheckLicenses, Unit.Full, IntegrationFull)
}

// Clean cleans up the directory.
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
