package build

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/cmakepolicy"
)

// This file is what a build backend runs its commands with: the Host of the
// sdkbuild.Context of a build, and the environment of its commands.
//
// The process environment is assembled on the exec.Cmd, never via os.Chdir
// or os.Setenv: the TT_* contract variables come last, so that a stray env
// of a build block cannot redirect TT_OUTPUT_DIR, say.

// pathKey is the environment variable programs are looked up in.
const pathKey = "PATH"

// ProjectBinDir is the directory a project keeps the programs it installs for
// itself in: <project>/.tarantool/bin, where tt binary --scope project links
// its binaries and a module links the tools it installs into the project
// (sdk.Project.StateDir). A build looks there first,
// so a component or a hook can name such a program as a bare command without
// the shell having sourced .env/activate.sh.
func ProjectBinDir(projectRoot string) string {
	return filepath.Join(projectRoot, scope.ProjectDirName, binary.BinDirName)
}

// environment is the environment of the commands of one build, of a
// component or of a hook, in its parts.
type environment struct {
	// block is the env of the build block.
	block map[string]string
	// projectRoot is the root of the project built, whose bin directory
	// (ProjectBinDir) goes first in PATH.
	projectRoot string
	// contract are the TT_* variables of the build, as key=value, in the
	// order the commands get them.
	contract []string
}

// environ is the environment of a command of the build: os.Environ() first,
// then the env of the block in key order, then PATH with the project's bin
// directory in front of it, when the project has one, and the TT_* contract
// variables last, so that exec.Cmd's last-wins resolution keeps them
// authoritative.
//
// The CMake policy version tt sets for the rocks it builds is left out of
// os.Environ(): a component is the user's code, configured with the policy
// version the user sets, if any (cmakepolicy.Strip).
func (e environment) environ() []string {
	out := cmakepolicy.Strip(os.Environ())

	for _, key := range slices.Sorted(maps.Keys(e.block)) {
		out = append(out, key+"="+e.block[key])
	}

	return append(withProjectBin(out, e.projectRoot), e.contract...)
}

// variables are the variables of the build as sdkbuild.Context.Env has them:
// those of the block, and the TT_* ones, which win.
func (e environment) variables() map[string]string {
	vars := maps.Clone(e.block)
	if vars == nil {
		vars = make(map[string]string, len(e.contract))
	}

	for _, entry := range e.contract {
		key, value, _ := strings.Cut(entry, "=")

		vars[key] = value
	}

	return vars
}

// contract are the TT_* variables of the build of a component or of a hook.
type contract struct {
	// outputDir is TT_OUTPUT_DIR; component is TT_COMPONENT_NAME; buildDir
	// is TT_BUILD_DIR. A hook has none of them.
	outputDir, component, buildDir string
	// projectRoot is TT_PROJECT_ROOT, pkg TT_PACKAGE and version
	// TT_VERSION.
	projectRoot, pkg, version string
	// target is TT_PLATFORM_OS and TT_PLATFORM_ARCH.
	target sdkbuild.Target
	// tarantool is TT_TARANTOOL_PREFIX and TT_TARANTOOL_INCLUDE_DIR, each set
	// only when tt knows it: a guessed path would send the build to look
	// for the headers where they are not, rather than let it fail or find
	// them itself.
	tarantool sdkbuild.Tarantool
	// hook is set for the reduced contract of a lifecycle hook, which runs
	// around all the components and has no single output directory,
	// component name or directory of intermediate files.
	hook bool
}

// environ is the contract as key=value: TT_OUTPUT_DIR, TT_PROJECT_ROOT,
// TT_PACKAGE, TT_COMPONENT_NAME, TT_VERSION, TT_PLATFORM_OS,
// TT_PLATFORM_ARCH, TT_BUILD_DIR, then those of Tarantool; those of a
// component left out for a hook.
func (c contract) environ() []string {
	var environ []string

	if !c.hook {
		environ = append(environ, "TT_OUTPUT_DIR="+c.outputDir)
	}

	environ = append(environ, "TT_PROJECT_ROOT="+c.projectRoot, "TT_PACKAGE="+c.pkg)

	if !c.hook {
		environ = append(environ, "TT_COMPONENT_NAME="+c.component)
	}

	environ = append(environ,
		"TT_VERSION="+c.version,
		"TT_PLATFORM_OS="+c.target.OS,
		"TT_PLATFORM_ARCH="+c.target.Arch,
	)

	if !c.hook && c.buildDir != "" {
		environ = append(environ, "TT_BUILD_DIR="+c.buildDir)
	}

	if c.tarantool.Prefix != "" {
		environ = append(environ, "TT_TARANTOOL_PREFIX="+c.tarantool.Prefix)
	}

	if c.tarantool.IncludeDir != "" {
		environ = append(environ, "TT_TARANTOOL_INCLUDE_DIR="+c.tarantool.IncludeDir)
	}

	return environ
}

// withProjectBin returns environ with the project's bin directory in front of
// its PATH, when the project has one. The entry is appended rather than
// edited in place: exec.Cmd keeps the last of duplicate keys, so the value
// below wins over the inherited one and over a PATH from the env of the
// block, which it extends rather than replaces.
func withProjectBin(environ []string, projectRoot string) []string {
	if projectRoot == "" {
		return environ
	}

	dir := ProjectBinDir(projectRoot)

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return environ
	}

	value := dir

	inherited, ok := sdkbuild.LookupEnv(environ, pathKey)
	if ok && inherited != "" {
		value += string(os.PathListSeparator) + inherited
	}

	return append(environ, pathKey+"="+value)
}

// lookPathIn finds the program name the way exec.LookPath does, but in the
// PATH of environ, the child's environment, and only there: that is where
// the project's bin directory was put, and a PATH the env of a build block
// sets is the whole of it, tt's own PATH no fallback. A name with a
// separator is a path and is returned as it is. A name found nowhere is an
// *exec.Error wrapping exec.ErrNotFound, in the words exec reports it with.
// Relative PATH entries are skipped: a program must not be picked up from
// wherever the build runs.
func lookPathIn(name string, environ []string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}

	value, _ := sdkbuild.LookupEnv(environ, pathKey)

	for _, dir := range filepath.SplitList(value) {
		if !filepath.IsAbs(dir) {
			continue
		}

		candidate := filepath.Join(dir, name)

		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}

	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// commandIn returns the command name with args, name looked up in the PATH
// of environ (lookPathIn). A name not there makes a command that fails to
// start with the error of the lookup, rather than one exec looks up in the
// PATH of tt.
func commandIn(ctx context.Context, environ []string, name string, args ...string) *exec.Cmd {
	program, lookErr := lookPathIn(name, environ)
	if lookErr != nil {
		program = name
	}

	cmd := exec.CommandContext(ctx, program, args...)
	if lookErr != nil {
		cmd.Path = name
		cmd.Err = lookErr
	}

	cmd.Args[0] = name
	cmd.Env = environ

	return cmd
}

// host runs the commands of one build for its sdkbuild.Context, as tt runs
// the commands of a build: with -V what they print is streamed, and
// otherwise kept and shown only when one fails, with a spinner while it
// runs.
type host struct {
	// env is the environment of the commands.
	env environment
	// cwd is the directory they run in.
	cwd string
	// showOutput streams what they print.
	showOutput bool
	// pinned are the versions the lock of the build pins, by rock.
	pinned map[string]string
}

// Command is the command name with args in the working directory of the
// build, with its environment, name looked up in its PATH (commandIn).
func (h host) Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := commandIn(ctx, h.env.environ(), name, args...)

	cmd.Dir = h.cwd

	return cmd
}

// Run runs cmd as util.RunCommand runs a command of tt.
func (h host) Run(cmd *exec.Cmd) error {
	logCommand(cmd)

	return util.RunCommand(cmd, cmd.Dir, h.showOutput)
}

// Output runs cmd as Run does, but returns its stdout, which is not shown:
// stderr alone is streamed or kept.
func (h host) Output(cmd *exec.Cmd) ([]byte, error) {
	logCommand(cmd)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	stopSpinner := func() {}

	if h.showOutput {
		cmd.Stderr = os.Stderr
	} else {
		stopSpinner = log.Spinner("running " + strings.Join(cmd.Args, " "))
	}

	err := cmd.Run()

	stopSpinner()

	if err != nil {
		_, _ = os.Stderr.Write(stderr.Bytes())

		return nil, fmt.Errorf("failed to run \n%s\n\n%w", cmd.String(), err)
	}

	return stdout.Bytes(), nil
}

// LookPath finds name in the PATH of the build, and only there
// (lookPathIn). A name with a separator is a path, relative to the working
// directory of the build, that must be an executable file.
func (h host) LookPath(name string) (string, error) {
	var (
		path string
		err  error
	)

	if strings.ContainsRune(name, os.PathSeparator) {
		path = name
		if !filepath.IsAbs(path) {
			path = filepath.Join(h.cwd, path)
		}

		path, err = exec.LookPath(path)
	} else {
		path, err = lookPathIn(name, h.env.environ())
	}

	if err != nil {
		return "", fmt.Errorf("looking for %s: %w", name, err)
	}

	return path, nil
}

// LockedVersion returns the version the lock of the build pins rock at.
func (h host) LockedVersion(rock string) (string, bool) {
	version, ok := h.pinned[rock]

	return version, ok
}

// logCommand logs the command a build or a hook is about to run and the
// directory it runs in, at the debug level tt -V shows. With -V the build
// streams what the command prints, and the command line, which only a
// failure showed, comes before it.
func logCommand(cmd *exec.Cmd) {
	log.Debug("running "+strings.Join(cmd.Args, " "), "dir", cmd.Dir)
}

// pinnedVersions are the versions closure pins, by rock.
func pinnedVersions(closure manifest.LockProduct) map[string]string {
	pinned := make(map[string]string, len(closure.Dependencies))

	for _, dep := range closure.Dependencies {
		pinned[dep.Name] = dep.Version
	}

	return pinned
}

// backendKey is the attribute that names the backend a record of a build
// comes from.
const backendKey = "backend"

// buildLogger is the logger of a build of the backend named backend
// (sdkbuild.Context.Log): the process logger, as the logger of a module is,
// with a backend=<name> attribute. It is not the logger of the options,
// which is that of go-luarocks and says library=go-luarocks.
func buildLogger(backend string) *slog.Logger {
	return log.Logger().With(backendKey, backend)
}
