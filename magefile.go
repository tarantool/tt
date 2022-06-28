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

	goExecutableName     = "go"
	pythonExecutableName = "python3"
	ttExecutableName     = "tt"

	generateModePath = filepath.Join(packagePath, "codegen", "generate_code.go")
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

// Patch cartridge-cli.
// Before building tt, we must apply patches for cartridge-cli.
// These patches contain code specific to cartridge-cli integration into tt
// and are not subject to commit to the cartridge's upstream.
func PatchCC() error {
	mg.Deps(GenCC)
	fmt.Printf("%s", "Apply cartridge-cli patches... ")

	patches_path := "../../extra/"
	patches := []string{
		"001_make_cmd_public.patch",
		"002_fix_admin_param.patch",
	}

	// Check that patch has been already applied.
	err := sh.Run(
		"patch", "-d", cartridgePath, "-N", "-p1", "--dry-run", "-V", "none",
		"-i", patches_path+patches[0],
	)

	if err != nil {
		fmt.Println("[already applied]")
		return nil
	}

	for _, patch := range patches {
		err = sh.Run(
			"patch", "-d", cartridgePath, "-N", "-p1", "-V", "none", "-i",
			patches_path+patch,
		)

		if err != nil {
			fmt.Println("[error]")
			return err
		}
	}

	fmt.Println("[done]")

	return nil
}

// Building tt executable.
func Build() error {
	fmt.Println("Building tt...")

	mg.Deps(PatchCC)
	mg.Deps(GenerateGoCode)

	err := sh.RunWith(
		getBuildEnvironment(), goExecutableName, "build",
		"-o", ttExecutableName,
		"-ldflags", strings.Join(ldflags, " "),
		"-asmflags", asmflags,
		"-gcflags", gcflags,
		packagePath,
	)

	if err != nil {
		return fmt.Errorf("Failed to build tt executable: %s", err)
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
		return sh.RunV(goExecutableName, "test", "-v", fmt.Sprintf("%s/...", packagePath))
	}

	return sh.RunV(goExecutableName, "test", fmt.Sprintf("%s/...", packagePath))
}

// Run integration tests.
func Integration() error {
	fmt.Println("Running integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "test/integration")
}

// Run all tests together.
func Test() {
	mg.SerialDeps(Lint, Unit, Integration)
}

// Cleanup directory.
func Clean() {
	fmt.Println("Cleaning directory...")

	os.Remove(ttExecutableName)
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
	}
}
