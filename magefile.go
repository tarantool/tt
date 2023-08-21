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
		"-s", "-w",
		"-X ${PACKAGE}/version.gitTag=${GIT_TAG}",
		"-X ${PACKAGE}/version.gitCommit=${GIT_COMMIT}",
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
	}

	for _, patch := range patches {
		err := applyPatch(patches_path + patch)
		if err != nil {
			return err
		}
	}

	return nil
}

// Building tt executable. Supported environment variables:
// TT_CLI_BUILD_SSL=(no|static|shared)
func Build() error {
	fmt.Println("Building tt...")

	mg.Deps(PatchCC)
	mg.Deps(GenerateGoCode)

	buildLdflags := make([]string, len(ldflags))
	copy(buildLdflags, ldflags)
	tags := "-tags=netgo,osusergo"

	buildType := os.Getenv(buildTypeEnv)
	switch BuildType(buildType) {
	case BuildTypeDefault:
		fallthrough
	case BuildTypeNoCgo:
		tags = tags + ",go_tarantool_ssl_disable"
	case BuildTypeStatic:
		if runtime.GOOS != "darwin" {
			buildLdflags = append(buildLdflags, staticLdflags...)
		}
		tags = tags + ",openssl_static"
	case BuildTypeShared:
	default:
		return fmt.Errorf("Unsupported build type: %s, supported: "+
			"%s, %s, %s\n",
			buildType, BuildTypeNoCgo, BuildTypeStatic, BuildTypeShared)
	}

	err := sh.RunWith(
		getBuildEnvironment(), goExecutableName, "build",
		"-o", ttExecutableName,
		tags,
		"-ldflags", strings.Join(buildLdflags, " "),
		"-asmflags", asmflags,
		"-gcflags", gcflags,
		packagePath,
	)

	if err != nil {
		return fmt.Errorf("Failed to build tt executable: %s", err)
	}

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

// Run golang and python linters.
func Lint() error {
	fmt.Println("Running golangci-lint...")

	mg.Deps(PatchCC)
	mg.Deps(GenerateGoCode)

	if err := sh.RunV("golangci-lint", "run", "--config=golangci-lint.yml"); err != nil {
		return err
	}

	fmt.Println("Running flake8...")

	if err := sh.RunV(pythonExecutableName, "-m", "flake8", "test"); err != nil {
		return err
	}

	return nil
}

// Run unit tests.
func Unit() error {
	fmt.Println("Running unit tests...")

	mg.Deps(GenerateGoCode)

	if mg.Verbose() {
		return sh.RunV(goExecutableName, "test", "-v",
			fmt.Sprintf("%s/...", packagePath))
	}

	return sh.RunV(goExecutableName, "test", fmt.Sprintf("%s/...", packagePath))
}

// Run unit tests with a Tarantool instance integration.
func UnitFull() error {
	fmt.Println("Running full unit tests...")

	mg.Deps(GenerateGoCode)

	if mg.Verbose() {
		return sh.RunV(goExecutableName, "test", "-v", fmt.Sprintf("%s/...", packagePath),
			"-tags", "integration")
	}

	return sh.RunV(goExecutableName, "test", fmt.Sprintf("%s/...", packagePath),
		"-tags", "integration")
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

	return sh.RunV("codespell", packagePath, "test", "README.md", "doc", "CHANGELOG.md",
		"--skip=./cli/cluster/paths.go")
}

// Run all tests together, excluding slow and unit integration tests.
func Test() {
	mg.SerialDeps(Lint, CheckLicenses, Unit, Integration)
}

// Run all tests together.
func TestFull() {
	mg.SerialDeps(Lint, CheckLicenses, UnitFull, IntegrationFull)
}

// Cleanup directory.
func Clean() {
	fmt.Println("Cleaning directory...")

	os.Remove(ttExecutableName)
}

// Generate generates code as usual `go generate` command. To work properly you
// will need a latest Tarantool executable in PATH.
func Generate() error {
	err := sh.RunWith(getBuildEnvironment(), goExecutableName, "generate", "./...")

	if err != nil {
		return err
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
	case "darwin":
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
	var gitCommit string

	if currentDir, err = os.Getwd(); err != nil {
		log.Warnf("Failed to get current directory: %s", err)
	}

	if _, err := exec.LookPath("git"); err == nil {
		gitTag, _ = sh.Output("git", "describe", "--tags")
		gitCommit, _ = sh.Output("git", "rev-parse", "--short", "HEAD")
	}

	return map[string]string{
		"PACKAGE":       goPackageName,
		"GIT_TAG":       gitTag,
		"GIT_COMMIT":    gitCommit,
		"VERSION_LABEL": os.Getenv("VERSION_LABEL"),
		"PWD":           currentDir,
		"CONFIG_PATH":   getDefaultConfigPath(),
		"CGO_ENABLED":   "1",
	}
}
