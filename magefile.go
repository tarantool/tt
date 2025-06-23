//go:build mage
// +build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/apex/log"
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

const (
	buildTypeEnv  = "TT_CLI_BUILD_SSL"
	goPackageName = "github.com/tarantool/tt/cli"

	asmflags = "all=-trimpath=${PWD}"
	gcflags  = "all=-trimpath=${PWD}"

	packagePath = "./cli"

	defaultLinuxConfigPath  = "/etc/tarantool"
	defaultDarwinConfigPath = "/usr/local/etc/tarantool"

	cartridgePath = "cli/cartridge/third_party/cartridge-cli"
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

	generateModePath = filepath.Join(packagePath, "codegen", "generate_code.go")

	Aliases = map[string]any{
		"build":    Build.Release,
		"unit":     Unit.Default,
		"unitfull": Unit.Full,
	}

	modules = []string{
		"lib/integrity",
		"lib/cluster",
	}
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

// Generate cartridge-cli Go code.
// Cartridge-cli uses code generator for Go.
// Accordingly, before building tt, we must generate this code.
func GenCC() {
	currDir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	err = os.Chdir(cartridgePath)
	if err != nil {
		panic(err)
	}

	err = sh.Run("go", "run", "cli/codegen/generate_code.go")
	if err != nil {
		panic(err)
	}

	err = os.Chdir(currDir)
	if err != nil {
		panic(err)
	}
}

// applyPatch applies the patch if it hasn't already been applied.
func applyPatch(path string) error {
	// Run the patch in dry run mode.
	out, err := sh.Output(
		"patch", "-d", cartridgePath, "-N", "-p1", "--dry-run", "-V", "none", "-i", path,
	)
	// If an error is returned, one of two things has happened:
	// the patch has already been applied or an error has occurred.
	if err != nil {
		if strings.Contains(out, "previously applied") {
			return nil
		}
		return err
	}

	err = sh.Run(
		"patch", "-d", cartridgePath, "-N", "-p1", "-V", "none", "-i", path,
	)

	fmt.Printf("* %s [done]\n", filepath.Base(path))

	return nil
}

// Patch cartridge-cli.
// Before building tt, we must apply patches for cartridge-cli.
// These patches contain code specific to cartridge-cli integration into tt
// and are not subject to commit to the cartridge's upstream.
func PatchCC() error {
	mg.Deps(GenCC)
	fmt.Printf("%s\n", "Apply cartridge-cli patches...")

	patches_path := "../../extra/"
	patches := []string{
		"001_make_cmd_public.patch",
		"002_fix_admin_param.patch",
		"003_fix_work_paths.patch",
		"004_fix_warning.patch",
		"005_rename_tt_env.patch",
		"006_consider_tt_run_dir.patch",
		"007_update_project_files_search_logic.patch",
		"008_increase_replicasets_bootstrap_attempts.patch",
		"009_support_1.14_copy_mod_version.patch",
	}

	for _, patch := range patches {
		err := applyPatch(patches_path + patch)
		if err != nil {
			return err
		}
	}

	return nil
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
		return []string{}, fmt.Errorf("Unsupported build type: %s, supported: "+
			"%s, %s, %s\n",
			buildType, BuildTypeNoCgo, BuildTypeStatic, BuildTypeShared)
	}
	return append(append(args, "-tags"), strings.Join(tags, ",")), nil
}

// Building tt executable. Supported environment variables:
// TT_CLI_BUILD_SSL=(no|static|shared).
func buildTt(argUpdaters ...optsUpdater) error {
	mg.Deps(PatchCC)
	mg.Deps(GenerateGoCode)

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
		return fmt.Errorf("Failed to build tt executable: %s", err)
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
	mg.Deps(Lint.Golang, Lint.Python)
	return nil
}

// Run golang linters.
func (Lint) Golang() error {
	fmt.Println("Running golangci-lint...")

	mg.Deps(PatchCC)
	mg.Deps(GenerateGoCode)

	lintDirs := append([]string{"."}, modules...)
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current dir: %w", err)
	}

	for _, dir := range lintDirs {
		os.Chdir(dir)
		if err := sh.RunV("golangci-lint", "run",
			fmt.Sprintf("--config=%s/golangci-lint.yml", root)); err != nil {
			return err
		}
		os.Chdir(root)
	}
	return nil
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
	mg.Deps(GenerateGoCode)

	testdirs := append([]string{"."}, modules...)
	for _, module := range testdirs {
		args := []string{"test", "-C", module}
		if mg.Verbose() {
			args = append(args, "-v")
		}
		args = append(args, "./...")
		args = append(args, flags...)

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

// Run codespell checks.
func Codespell() error {
	fmt.Println("Running codespell tests...")

	return sh.RunV("codespell", ".")
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

// GenerateGoCode generates code from lua files.
func GenerateGoCode() error {
	err := sh.RunWith(getBuildEnvironment(), goExecutableName, "run", generateModePath)
	if err != nil {
		return err
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
