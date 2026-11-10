//go:build mage

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"

	"github.com/tarantool/tt/v3/internal/buildflags"
)

var (
	errCannotParseAVersionOutOf                  = errors.New("cannot parse a version out of ")
	errIsGolangciLintWantTheProjectPinnedVersion = errors.New(" is golangci-lint ")
	errIsNotADirectory                           = errors.New(" is not a directory")
	errFuzzTestsFailed                           = errors.New("fuzz tests failed")
)

// spell-checker:ignore trimpath extldflags asmflags covdata GOEXE TTEXE GOCOVERDIR

const (
	asmflags = "all=-trimpath=${PWD}"
	gcflags  = "all=-trimpath=${PWD}"

	packagePath = "./cli"

	// tagsFlag is the go flag that sets the build tags.
	tagsFlag = "-tags"

	testDir = "test"

	lintConfig  = ".golangci.yml"
	lintVersion = "2.14.0"
	lintExeEnv  = "GOLANGCI_LINT"
)

var (
	// ldflags stamp the version of the checkout, which the environment of
	// getBuildEnvironment expands, into the binary.
	ldflags = buildflags.Version{
		Tag:             "${GIT_TAG}",
		Commit:          "${GIT_COMMIT}",
		CommitsSinceTag: "${GIT_COMMIT_SINCE_TAG}",
		Label:           "${VERSION_LABEL}",
	}.Ldflags()
	goExecutableName     = "go"
	pythonExecutableName = "python3"
	ttExecutableName     = "tt"

	Aliases = map[string]any{
		"build":    Build.Release,
		"unit":     Unit.Default,
		"unitfull": Unit.Full,
	}

	// lintVersionRe pulls the version out of `golangci-lint version`, which
	// prints e.g. "golangci-lint has version 2.14.0 built with go1.27.1 from ...".
	lintVersionRe = regexp.MustCompile(`\bversion v?(\d+)\.(\d+)\.(\d+)\b`)
)

func init() {
	var err error

	if specifiedGoExe := os.Getenv("GOEXE"); specifiedGoExe != "" {
		goExecutableName = specifiedGoExe
	}

	if specifiedTTExe := os.Getenv("TTEXE"); specifiedTTExe != "" {
		ttExecutableName = specifiedTTExe
	} else {
		ttExecutableName, err = filepath.Abs(ttExecutableName)
		if err != nil {
			panic(err)
		}
	}

	// We want to use Go 1.11 modules even if the source lives inside GOPATH.
	// The default is "auto".
	_ = os.Setenv("GO111MODULE", "on")
}

// findModules discovers project modules, including the root module. Test fixtures,
// vendored dependencies and local environments are not project modules.
func findModules() ([]string, error) {
	var modules []string

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			switch entry.Name() {
			case testDir, "third_party", "vendor":
				return filepath.SkipDir
			}

			if path != "." && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.Name() == "go.mod" {
			modules = append(modules, filepath.Dir(path))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to discover Go modules: %w", err)
	}

	return modules, nil
}

// lintModules returns the modules the linters check: the project modules and
// the Aeon mock server. The server is an integration fixture, so it is kept out
// of the project module list used for unit tests or generation.
func lintModules() ([]string, error) {
	modules, err := findModules()
	if err != nil {
		return nil, err
	}

	return append(modules, "test/integration/aeon/server"), nil
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
		ssl, err := buildflags.Parse(os.Getenv(buildflags.Env))
		if err != nil {
			return nil, err
		}

		buildLdflags := append([]string(nil), ldflags...)

		buildLdflags = append(buildLdflags, flags...)
		buildLdflags = append(buildLdflags, ssl.Ldflags(runtime.GOOS)...)

		return append(append(args, "-ldflags"), strings.Join(buildLdflags, " ")), nil
	}
}

// appendTags appends the build tags of the TLS implementation
// TT_CLI_BUILD_SSL selects (default, openssl or openssl_static; unset is
// default).
func appendTags(args []string) ([]string, error) {
	ssl, err := buildflags.Parse(os.Getenv(buildflags.Env))
	if err != nil {
		return nil, err
	}

	return append(append(args, tagsFlag), strings.Join(ssl.Tags(), ",")), nil
}

// Building tt executable. Supported environment variables:
// TT_CLI_BUILD_SSL=(default|openssl|openssl_static); unset aliases default.
func buildTt(argUpdaters ...optsUpdater) error {
	const buildArgsCapacity = 8

	args := make([]string, 0, buildArgsCapacity)

	args = append(args, "build", "-o", ttExecutableName)

	var err error

	for _, updateArguments := range argUpdaters {
		args, err = updateArguments(args)
		if err != nil {
			return err
		}
	}

	args = append(args,
		"-asmflags", asmflags,
		"-gcflags", gcflags,
		packagePath)

	err = sh.RunWith(getBuildEnvironment(), goExecutableName, args...)
	if err != nil {
		return fmt.Errorf("failed to build tt executable: %w", err)
	}

	return nil
}

type Build mg.Namespace

// Release builds the release tt executable without debug info.
func (Build) Release() error {
	_, _ = fmt.Fprintln(os.Stdout, "Building release tt...")

	return buildTt(appendTags, appendLdFlags("-s", "-w"))
}

// Debug builds the debug tt executable.
func (Build) Debug() error {
	_, _ = fmt.Fprintln(os.Stdout, "Building debug tt...")

	return buildTt(appendTags, appendLdFlags())
}

// Coverage builds the tt executable with coverage.
func (Build) Coverage() error {
	_, _ = fmt.Fprintln(os.Stdout, "Building release tt with coverage...")

	err := buildTt(appendFlags("-cover", "-covermode=atomic"), appendTags,
		appendLdFlags("-s", "-w"))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(os.Stdout, `Set coverage data destination directory (must exist) and run tt:
	GOCOVERDIR=./<coverage_dest_dir> tt <opts>`)

	return nil
}

// CheckLicenses runs the license checker.
func CheckLicenses() error {
	_, _ = fmt.Fprintln(os.Stdout, "Running license checker...")

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get the home directory: %w", err)
	}

	return sh.RunV(home+"/go/bin/lichen", "--config", ".lichen.yaml", "tt")
}

// mageArgs keeps optional task flags before Mage parses its target list.
var mageArgs = slices.Clone(os.Args[1:])

func init() {
	pythonLint := false

	for _, argument := range mageArgs {
		if strings.EqualFold(argument, "lint:python") || strings.EqualFold(argument, "lint:full") {
			pythonLint = true
		}
	}

	if !pythonLint {
		return
	}

	arguments := []string{os.Args[0]}

	for _, argument := range mageArgs {
		if argument != "--fix" {
			arguments = append(arguments, argument)
		}
	}

	os.Args = arguments
}

func fixFlag() bool {
	return slices.Contains(mageArgs, "--fix")
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

	_, _ = fmt.Fprintf(os.Stdout, "Running %s over %s...\n", linter, lintConfig)

	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current dir: %w", err)
	}

	modules, err := lintModules()
	if err != nil {
		return err
	}

	for _, dir := range modules {
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
			"%s%w%s, want the project-pinned version %s",
			linter, errIsGolangciLintWantTheProjectPinnedVersion, version, lintVersion)
	}

	return linter, nil
}

// linterVersionOf runs `<exe> version` and reports its semantic version.
func linterVersionOf(exe string) (string, error) {
	_, err := exec.LookPath(exe)
	if err != nil {
		return "", fmt.Errorf("not found: %w", err)
	}

	out, err := sh.Output(exe, "version")
	if err != nil {
		return "", fmt.Errorf("running %q version: %w", exe, err)
	}

	match := lintVersionRe.FindStringSubmatch(out)
	if match == nil {
		return "", fmt.Errorf("%w%q", errCannotParseAVersionOutOf, strings.TrimSpace(out))
	}

	return match[1] + "." + match[2] + "." + match[3], nil
}

// Python runs python linters.
func (Lint) Python() error {
	_, _ = fmt.Fprintln(os.Stdout, "Running Ruff...")

	arguments := []string{"-m", "ruff", "check", testDir}
	if fixFlag() {
		arguments = append(arguments, "--fix")
	}

	err := sh.RunV(pythonExecutableName, arguments...)
	if err != nil {
		return err
	}

	arguments = []string{"-m", "ruff", "format", testDir}
	if !fixFlag() {
		arguments = append(arguments, "--check")
	}

	return sh.RunV(pythonExecutableName, arguments...)
}

// Modules checks that project module files are tidy and dependencies are intact.
func (Lint) Modules() error {
	modules, err := lintModules()
	if err != nil {
		return err
	}

	for _, module := range modules {
		// -diff reports drift without changing go.mod or go.sum.
		err = sh.RunV(goExecutableName, "-C", module, "mod", "tidy", "-diff")
		if err != nil {
			return err
		}

		err = sh.RunV(goExecutableName, "-C", module, "mod", "verify")
		if err != nil {
			return err
		}
	}

	return nil
}

// Vuln checks every project module with the govulncheck installed by CI.
func (Lint) Vuln() error {
	modules, err := findModules()
	if err != nil {
		return err
	}

	var failures []error

	for _, module := range modules {
		_, _ = fmt.Fprintf(os.Stdout, "Checking vulnerabilities in %s...\n", module)

		err = sh.RunV("govulncheck", "-C", module, "./...")
		if err != nil {
			failures = append(failures, fmt.Errorf("govulncheck %s: %w", module, err))
		}
	}

	return errors.Join(failures...)
}

type Unit mg.Namespace

func runUnitTests(flags []string) error {
	modules, err := findModules()
	if err != nil {
		return err
	}

	for _, module := range modules {
		args := []string{"test", "-C", module}
		if mg.Verbose() {
			args = append(args, "-v")
		}

		// Go flags must precede -args, which forwards the remaining arguments
		// directly to the test binary (used by Unit.Coverage).
		args = append(args, "./...", "-count=1")
		args = append(args, flags...)

		err := sh.RunV(goExecutableName, args...)
		if err != nil {
			return err
		}
	}

	return nil
}

// Default runs unit tests.
func (Unit) Default() error {
	_, _ = fmt.Fprintln(os.Stdout, "Running unit tests...")

	return runUnitTests([]string{})
}

// Race runs unit tests in every project module with the race detector.
func (Unit) Race() error {
	return runUnitTests([]string{"-race"})
}

// The variable that sets the time mage unit:fuzz fuzzes each test for, a
// duration as go test -fuzztime takes it, and the time when it is not set.
const (
	fuzzTimeEnv     = "FUZZTIME"
	defaultFuzzTime = "1m"
)

// fuzzTest is a fuzz test of a package of a module.
type fuzzTest struct {
	module, pkg, name string
}

// Fuzz fuzzes every fuzz test of every project module for FUZZTIME, a minute
// by default, one after another, as go test fuzzes one test at a time. The
// inputs that find new code are kept in the fuzz cache under GOCACHE, so a
// next run goes on from them; an input that fails a test is written to
// testdata/fuzz of its package. The tests that fail are reported at the end.
func (Unit) Fuzz() error {
	fuzzTime := os.Getenv(fuzzTimeEnv)
	if fuzzTime == "" {
		fuzzTime = defaultFuzzTime
	}

	modules, err := findModules()
	if err != nil {
		return err
	}

	var failed []string

	for _, module := range modules {
		tests, err := fuzzTests(module)
		if err != nil {
			return err
		}

		for _, test := range tests {
			_, _ = fmt.Fprintf(os.Stdout, "Fuzzing %s %s for %s...\n",
				test.pkg, test.name, fuzzTime)

			err := sh.RunV(goExecutableName, "test", "-C", test.module, "-run", "^$",
				"-fuzz", "^"+test.name+"$", "-fuzztime", fuzzTime, test.pkg)
			if err != nil {
				failed = append(failed, test.pkg+" "+test.name)
			}
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("%w:\n%s", errFuzzTestsFailed, strings.Join(failed, "\n"))
	}

	return nil
}

// fuzzTests lists the fuzz tests of the packages of module: go test -list
// prints the names of a package, then its ok line.
func fuzzTests(module string) ([]fuzzTest, error) {
	out, err := sh.Output(goExecutableName, "test", "-C", module, "-list", "^Fuzz", "./...")
	if err != nil {
		return nil, fmt.Errorf("failed to list the fuzz tests of %s: %w", module, err)
	}

	var (
		tests []fuzzTest
		names []string
	)

	for line := range strings.Lines(out) {
		fields := strings.Fields(line)

		switch {
		case len(fields) == 1 && strings.HasPrefix(fields[0], "Fuzz"):
			names = append(names, fields[0])
		case len(fields) >= 2 && fields[0] == "ok":
			for _, name := range names {
				tests = append(tests, fuzzTest{module: module, pkg: fields[1], name: name})
			}

			names = nil
		}
	}

	return tests, nil
}

// Full runs unit tests with Tarantool instance integration. TT_TEST_TARANTOOL
// picks the Tarantool, see magefile.tarantool.go.
func (Unit) Full() error {
	mg.Deps(useTestTarantool)

	_, _ = fmt.Fprintln(os.Stdout, "Running full unit tests...")

	return runUnitTests([]string{tagsFlag, "integration,integration_docker"})
}

// FullSkipDocker runs unit tests with Tarantool instance integration, excluding docker tests.
func (Unit) FullSkipDocker() error {
	mg.Deps(useTestTarantool)

	_, _ = fmt.Fprintln(os.Stdout, "Running full unit tests, excluding docker...")

	return runUnitTests([]string{tagsFlag, "integration"})
}

// Coverage runs the full unit test set with code coverage.
func (Unit) Coverage() error {
	mg.Deps(useTestTarantool)

	_, _ = fmt.Fprintln(os.Stdout, "Running full unit tests with code coverage...")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current dir: %w", err)
	}

	coverDir := filepath.Join(cwd, "coverage", "unit")

	err = ensureCoverageDir(coverDir)
	if err != nil {
		return err
	}

	err = runUnitTests([]string{
		tagsFlag, "integration,integration_docker",
		"-cover", "-covermode=atomic",
		"-args", "-test.gocoverdir=" + coverDir,
	})
	if err != nil {
		return err
	}

	relCoverDir, err := filepath.Rel(cwd, coverDir)
	if err != nil {
		relCoverDir = coverDir
	}

	_, _ = fmt.Fprintf(os.Stdout, "Coverage data is saved to %q\n", relCoverDir)
	_, _ = fmt.Fprintf(os.Stdout, `Example command for analysis:
	go tool covdata func -i %q
`, relCoverDir)

	return nil
}

func ensureCoverageDir(coverDir string) error {
	const coverageDirectoryMode = 0o750

	coverageDirInfo, err := os.Stat(coverDir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(coverDir, coverageDirectoryMode)
		if err != nil {
			return fmt.Errorf("failed to create %q: %w", coverDir, err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to stat %q: %w", coverDir, err)
	}

	if !coverageDirInfo.IsDir() {
		return fmt.Errorf("%q%w", coverDir, errIsNotADirectory)
	}

	return nil
}

// Integration runs integration tests, excluding slow tests. TT_TEST_TARANTOOL
// picks the Tarantool, see magefile.tarantool.go.
func Integration() error {
	mg.Deps(useTestTarantool)

	_, _ = fmt.Fprintln(os.Stdout, "Running integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "not slow and not slow_ee "+
		"and not notarantool and not tarantool_master", "test/integration")
}

// IntegrationFull runs the full set of integration tests.
func IntegrationFull() error {
	mg.Deps(useTestTarantool)

	_, _ = fmt.Fprintln(os.Stdout, "Running all integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m",
		"not slow_ee and not notarantool and not tarantool_master",
		"test/integration")
}

// IntegrationFullSkipDocker runs the full set of integration tests, excluding docker tests.
func IntegrationFullSkipDocker() error {
	mg.Deps(useTestTarantool)

	_, _ = fmt.Fprintln(os.Stdout, "Running all integration tests, excluding docker...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m",
		"not slow_ee and not notarantool and not docker and not tarantool_master",
		"test/integration")
}

// IntegrationFullDocker runs only docker tests from the full set.
func IntegrationFullDocker() error {
	mg.Deps(useTestTarantool)

	_, _ = fmt.Fprintln(os.Stdout, "Running docker integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m",
		"not slow_ee and not notarantool and docker and not tarantool_master", "test/integration")
}

// IntegrationEE runs the set of EE integration tests.
func IntegrationEE() error {
	_, _ = fmt.Fprintln(os.Stdout, "Running all EE integration tests...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "test/integration/ee")
}

// IntegrationNoTarantool runs integration tests without a system-wide Tarantool installation.
func IntegrationNoTarantool() error {
	_, _ = fmt.Fprintln(os.Stdout, "Running integration tests without Tarantool...")

	return sh.RunV(pythonExecutableName, "-m", "pytest", "-m", "notarantool",
		"test/integration")
}

// CodeSpell runs code spell checks.
func CodeSpell() error {
	_, _ = fmt.Fprintln(os.Stdout, "Running code spell tests...")

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
	_, _ = fmt.Fprintln(os.Stdout, "Cleaning directory...")

	_ = os.Remove(ttExecutableName)
}

// Generate runs go generate with stringer available in PATH.
func Generate() error {
	modules, err := findModules()
	if err != nil {
		return err
	}

	for _, path := range modules {
		err := sh.RunWith(getBuildEnvironment(), goExecutableName, "-C", path,
			"generate", "./...")
		if err != nil {
			return fmt.Errorf("failed to generate sources for path %q: %w", path, err)
		}
	}

	return nil
}

// commandReferenceDir is where the documentation keeps the generated command
// reference.
const commandReferenceDir = "doc/reference/commands"

type Docs mg.Namespace

// Reference generates the command reference of the documentation from the
// help of tt.
func (Docs) Reference() error {
	_, _ = fmt.Fprintln(os.Stdout, "Generating the command reference...")

	dir, err := filepath.Abs(commandReferenceDir)
	if err != nil {
		return fmt.Errorf("cannot resolve the reference directory: %w", err)
	}

	// Pages of commands tt no longer has must not linger.
	err = os.RemoveAll(dir)
	if err != nil {
		return fmt.Errorf("cannot clean the reference directory: %w", err)
	}

	return sh.RunV(goExecutableName, "test", "./core", "-run", "^TestCLIReference$",
		"-count=1", "-args", "-clidoc", dir)
}

// Build builds the documentation site into site/, failing on a broken link
// or a page the nav omits. MkDocs comes from doc/requirements.txt.
func (Docs) Build() error {
	mg.Deps(Docs.Reference)

	return sh.RunV(pythonExecutableName, "-m", "mkdocs", "build", "--strict")
}

// Serve serves the documentation site with live reload on
// http://127.0.0.1:8000/tt/.
func (Docs) Serve() error {
	mg.Deps(Docs.Reference)

	return sh.RunV(pythonExecutableName, "-m", "mkdocs", "serve")
}

// Stale lists the translated pages of the documentation whose English page
// changed after them or no longer exists. It only reports.
func (Docs) Stale() error {
	return sh.RunV(pythonExecutableName, "ci/doc_translations.py")
}

// getBuildEnvironment return map with build environment variables.
func getBuildEnvironment() map[string]string {
	var err error

	var (
		currentDir        string
		gitTag            string
		gitTagShort       string
		gitCommit         string
		gitCommitSinceTag string
	)

	currentDir, err = os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get current directory: %s\n", err)
	}

	_, err = exec.LookPath("git")
	if err == nil {
		gitTag, _ = sh.Output("git", "describe", "--tags")
		gitTagShort, _ = sh.Output("git", "describe", "--tags", "--abbrev=0")
		gitCommit, _ = sh.Output("git", "rev-parse", "--short", "HEAD")
		gitCommitSinceTag, _ = sh.Output("git", "rev-list", gitTagShort+"..", "--count")
	}

	return map[string]string{
		"GIT_TAG":              gitTag,
		"GIT_COMMIT":           gitCommit,
		"GIT_COMMIT_SINCE_TAG": gitCommitSinceTag,
		"VERSION_LABEL":        os.Getenv("VERSION_LABEL"),
		"PWD":                  currentDir,
		"CGO_ENABLED":          cgoEnabled(),
	}
}

// cgoEnabled returns CGO_ENABLED for the TLS implementation TT_CLI_BUILD_SSL
// selects: "1" for the OpenSSL ones, which link OpenSSL through cgo, and "0"
// for the default, whose binary is pure Go and runs whatever the C library
// of the machine. tt binary build tt decides the same way. An invalid value
// is "0": the build reports it before it runs.
func cgoEnabled() string {
	ssl, err := buildflags.Parse(os.Getenv(buildflags.Env))
	if err != nil || !ssl.Cgo() {
		return "0"
	}

	return "1"
}
