package cmd

import (
	"context"
	"errors"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/build"
	"github.com/tarantool/tt/cli/manifest/run"
	ttversion "github.com/tarantool/tt/cli/version"
)

// luatestName is the rock tt test runs, and the requirement it adds implicitly
// when the manifest declares none.
const luatestName = "luatest"

// NewTestCmd creates `tt test`: build the package, then run its test suite
// under the same Tarantool tt run would choose.
func NewTestCmd() *cobra.Command {
	testCmd := &cobra.Command{
		Use:   "test [SUB-PATH] [-- LUATEST-ARGS...]",
		Short: "Build the current package and run its tests",
		Long: `Build the package in the current directory and run its tests with luatest.

The directory must hold app.manifest.toml; no tt environment and no tt.yaml are
involved. Tests are taken from test/, or from tests/ when there is no test/. A
SUB-PATH relative to the project narrows the run to one directory or one file,
and must exist.

The build runs first, so components are compiled and .rocks/ holds the declared
dependencies before any test does. luatest itself need not be declared: when
[dev_dependencies] does not name it and the project tree does not already hold
it, it is resolved as an implicit '*' requirement and installed. That resolution
is not a declaration — app.manifest.toml and app.manifest.lock are left exactly
as they were, so running the tests never makes the lock stale.

Everything after '--' is handed to luatest, and the exit code is luatest's own.
`,
		Args: cobra.ArbitraryArgs,
		Run: func(cmd *cobra.Command, args []string) {
			subPath, luatestArgs, err := splitTestArgs(cmd, args)
			if err == nil {
				err = runTest(subPath, luatestArgs)
			}

			if err != nil {
				log.Error(err.Error())
				os.Exit(build.ExitCode(err))
			}
		},
		Example: `
# Build and run everything under test/ (or tests/).

    $ tt test

# Run one directory, then one file.

    $ tt test test/integration
    $ tt test test/unit/config_test.lua

# Pass luatest's own options through.

    $ tt test -- --verbose --shuffle group
`,
	}

	return testCmd
}

// errTooManyTestPaths reports more than one path before '--'.
var errTooManyTestPaths = errors.New(
	"tt test takes at most one sub-path; put luatest's own options after '--'")

// splitTestArgs separates the optional sub-path from luatest's own arguments.
//
// Cobra records where '--' appeared rather than dropping the information, so
// the split needs no manual scan: everything before it belongs to tt, and
// everything after it to luatest — including flags tt itself defines, which is
// the point of requiring the separator.
func splitTestArgs(cmd *cobra.Command, args []string) (string, []string, error) {
	own := args
	passed := []string(nil)

	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		own = args[:dash]
		passed = args[dash:]
	}

	switch len(own) {
	case 0:
		return "", passed, nil
	case 1:
		return own[0], passed, nil
	default:
		return "", nil, errTooManyTestPaths
	}
}

// runTest builds the package and hands the suite to luatest.
func runTest(subPath string, luatestArgs []string) error {
	root, tarantool, err := selectProjectTarantool(&cmdCtx)
	if err != nil {
		return err
	}

	// Resolved before the build so a typo in the path costs nothing: a build is
	// the expensive step, and its result is the same either way.
	testDir, err := run.TestDir(root, subPath)
	if err != nil {
		return err
	}

	tntInfo, err := tarantoolInfo()
	if err != nil {
		return err
	}

	opts := build.Options{
		ProjectDir: root,
		TtVersion:  "tt " + ttversion.GetVersion(true, false),
		Tarantool:  tntInfo,
		ShowOutput: cmdCtx.Cli.Verbose,
		Warn:       func(msg string) { log.Warn(msg) },
	}

	ctx := context.Background()

	if err := build.Run(ctx, opts); err != nil {
		return err
	}

	if err := ensureLuatest(ctx, root, opts); err != nil {
		return err
	}

	script, err := run.LuatestScript(root)
	if err != nil {
		return err
	}

	argv := append([]string{script, testDir}, luatestArgs...)

	return run.Exec(root, tarantool, argv)
}

// ensureLuatest puts a runner in the project tree when the build did not.
//
// A tree that already holds one is left alone rather than re-resolved: the
// implicit requirement is "*", so any installed version satisfies it, and
// re-resolving would reach a registry on every single test run.
func ensureLuatest(ctx context.Context, root string, opts build.Options) error {
	if run.LuatestInstalled(root) {
		return nil
	}

	// An unset source is the registry, exactly as it is for the manifest's own
	// short form where a dependency is written as a bare constraint string.
	return build.EnsureDevRocks(ctx, opts, map[string]manifest.Dependency{
		luatestName: {Version: "*"},
	})
}
