package cmd

import (
	"context"
	"slices"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/run"
)

// luatestName is the rock tt test runs, and the requirement it adds implicitly
// when the manifest declares none.
const luatestName = "luatest"

// NewTestCmd creates `tt test`: build the package, then run its test suite
// under the same Tarantool tt run would choose.
func NewTestCmd() *cobra.Command {
	testCmd := &cobra.Command{
		Use:   "test [SUB-PATH...] [-- LUATEST-ARGS...]",
		Short: "Build the current package and run its tests",
		Long: `Build the package in the current directory and run its tests with luatest.

The directory must hold app.manifest.toml; no tt environment and no tt.yaml are
involved. Tests are taken from test/, or from tests/ when there is no test/.
SUB-PATHs relative to the project narrow the run to those directories and files,
in the order given, and each must exist.

The build runs first, so components are compiled and .rocks/ holds the declared
dependencies before any test does. luatest itself need not be declared: when
[dev_dependencies] does not name it and the project tree does not already hold
it, it is resolved as an implicit '*' requirement and installed. That resolution
is not a declaration — app.manifest.toml and app.manifest.lock are left exactly
as they were, so running the tests never makes the lock stale.

Everything after '--' is handed to luatest, and the exit code is luatest's own.
`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			subPaths, luatestArgs := splitTestArgs(cmd, args)

			return commandError(cmd, runTest(subPaths, luatestArgs))
		},
		Example: `# Build and run everything under test/ (or tests/).
  $ tt test

  # Run one directory, then one file, then two directories in one run.
  $ tt test test/integration
  $ tt test test/unit/config_test.lua
  $ tt test test/unit test/integration

  # Pass luatest's own options through.
  $ tt test -- --verbose --shuffle group`,
	}

	addProjectDirFlag(testCmd)

	return testCmd
}

// splitTestArgs separates the sub-paths from luatest's own arguments.
//
// Cobra records where '--' appeared rather than dropping the information, so
// the split needs no manual scan: everything before it is a sub-path of tt's,
// and everything after it goes to luatest — including flags tt itself defines,
// which is the point of requiring the separator. Put before it, a luatest
// option is a flag tt does not know, and a group name is a sub-path that does
// not exist: both fail before the build.
func splitTestArgs(cmd *cobra.Command, args []string) ([]string, []string) {
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		return args[:dash], args[dash:]
	}

	return args, nil
}

// runTest builds the package and hands the suite to luatest.
func runTest(subPaths, luatestArgs []string) error {
	root, tarantool, err := selectProjectTarantool()
	if err != nil {
		return err
	}

	// Resolved before the build so a typo in a path costs nothing: a build is
	// the expensive step, and its result is the same either way.
	testPaths, err := run.TestPaths(root, subPaths)
	if err != nil {
		return err
	}

	tntInfo, err := tarantoolInfo()
	if err != nil {
		return err
	}

	sources, err := registrySources(packageRegistries)
	if err != nil {
		return err
	}

	opts := build.Options{
		ProjectDir: root,
		Product:    "",
		Component:  "",
		Locked:     false,
		FetchOnly:  false,
		TtVersion:  lockStamp(),
		Tarantool:  tntInfo,
		Registries: sources,
		ShowOutput: cmdCtx.Cli.Verbose,
		Rebuild:    false,
		Now:        time.Time{},
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
		Check:      nil,
	}

	// The build of tt test runs build tools as tt package build does, and
	// they are killed as its are.
	ctx, stop := packageContext()
	defer stop()

	err = build.Run(ctx, opts)
	if err != nil {
		return err
	}

	err = ensureLuatest(ctx, root, opts)
	if err != nil {
		return err
	}

	script, err := run.LuatestScript(ctx, root)
	if err != nil {
		return err
	}

	// luatest takes its options and its paths in any order, so the paths of tt
	// lead and what follows '--' is appended as it was typed.
	argv := slices.Concat([]string{script}, testPaths, luatestArgs)

	// luatest gets the signals: tt is replaced by it.
	stop()

	return run.Exec(root, tarantool, argv)
}

// ensureLuatest puts a runner in the project tree when the build did not.
//
// A tree that already holds one is left alone rather than re-resolved: the
// implicit requirement is "*", so any installed version satisfies it, and
// re-resolving would reach a registry on every single test run.
func ensureLuatest(ctx context.Context, root string, opts build.Options) error {
	if run.LuatestInstalled(ctx, root) {
		return nil
	}

	// An unset source is the registry, exactly as it is for the manifest's own
	// short form where a dependency is written as a bare constraint string.
	return build.EnsureDevRocks(ctx, opts, map[string]manifest.Dependency{
		luatestName: {Source: "", Version: "*", Path: "", Registry: "", Kind: ""},
	})
}
