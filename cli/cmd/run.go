package cmd

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/scope"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/manifest/run"
)

// NewRunCmd creates `tt run`: the project's Tarantool, with the arguments
// passed straight through.
func NewRunCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run [TARANTOOL-OPTIONS...] [SCRIPT [ARGS...]]",
		Short: "Run the Tarantool of the current project",
		Long: `Run the Tarantool of the current project, in the current directory.

The project is the one the current directory is in, as the instance commands
find it: the nearest directory, the current one or one above it, holding
app.manifest.toml or the pair of a template in .templates/<name>/, as
tt create single_instance makes. No package is needed, and no tt environment
and no tt.yaml are involved. The interpreter is the project's own, the one
.tarantool/bin/tarantool of the project selects, and the first tarantool on
PATH when there is none — TT_USE_SYSTEM_TARANTOOL=1 forces PATH either way.
tt binary install and tt binary switch set that link; a with-deps
tt package install sets it to the runtime the package bundles when the project
selects no tarantool yet.

Tarantool runs in the current directory, as if started there by hand, so a
script path is relative to it: 'tt run ../script.lua' in a subdirectory runs
the script of the parent directory. Tarantool finds the project's .rocks/,
dependencies and C modules included, by searching upward from the directory
it runs in. Below the project root, tt puts the root at the head of LUA_PATH
and LUA_CPATH, so the project's own sources, such as init.lua of the root,
resolve as they do there; in the root, the environment is passed unchanged.

tt reads its own options, -h and --help, only among the options before the
script name, and a '--' there ends them and is dropped. Every other argument
goes to Tarantool untouched, and everything from the script name on, -h and
'--' included, is the script's. tt replaces itself with the interpreter, so
signals and the exit code are Tarantool's own.
`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			help, argv := splitRunArgs(args)
			if help {
				return cmd.Help()
			}

			return internalRunE(internalRunModule)(cmd, argv)
		},
		Example: `# Start an interactive console in the project.
  $ tt run

  # Print the version of the Tarantool the project runs under:
  $ tt run --version
  Tarantool 3.0.0-entrypoint-724-gd2d7f4de3
  . . .

  # Run a script with arguments. Everything after the script name is the
  # script's, -h and '--' included, because Tarantool itself is what reads
  # them:
  $ tt run script.lua a b c
  a	b	c
  $ tt run script.lua -- -h
  --	-h

  # Run a script of the parent directory from a subdirectory of the project:
  $ cd test && tt run ../script.lua

  # Hand Tarantool's own -h over to it:
  $ tt run -- -h

  # Execute stdin:
  $ echo 'print(42)' | tt run -

  # Ignore the project's tarantool and use the one on PATH:
  $ TT_USE_SYSTEM_TARANTOOL=1 tt run script.lua`,
		DisableFlagsInUseLine: true,
	}

	return runCmd
}

// tarantoolValueOptions are the options of Tarantool that take the next
// argument as their value, which is no script name even when it does not
// start with '-'.
var tarantoolValueOptions = map[string]bool{ //nolint:gochecknoglobals // A constant set.
	"-e": true, "-l": true, "-j": true,
	"-n": true, "--name": true,
	"-c": true, "--config": true,
	"--integrity-check": true,
}

// splitRunArgs reads the options of tt run itself out of args: whether help
// was asked for, and the arguments to hand to Tarantool.
//
// tt reads only the options before the script name, Tarantool's first
// argument that is neither an option nor the value of one, and of them only
// -h and --help are its own; '-' is a script name too, stdin. A '--' among
// them ends them and is dropped, so that 'tt run -- -h' asks Tarantool for
// its help. From the script name on, everything is the script's, -h and '--'
// included. Tarantool's -b takes all the arguments after it, so nothing
// after it is read either.
func splitRunArgs(args []string) (bool, []string) {
	for pos := 0; pos < len(args); pos++ {
		arg := args[pos]

		switch {
		case arg == "--":
			return false, slices.Concat(args[:pos], args[pos+1:])
		case arg == "-h" || arg == "--help":
			return true, nil
		case arg == "-" || !strings.HasPrefix(arg, "-") || arg == "-b":
			return false, args
		case tarantoolValueOptions[arg]:
			pos++
		}
	}

	return false, args
}

// internalRunModule is a default run module.
func internalRunModule(_ *cmdcontext.CmdCtx, args []string) error {
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	root, err := anyProjectRoot(projectDir)
	if err != nil {
		return err
	}

	tarantool, err := projectTarantool(root)
	if err != nil {
		return err
	}

	return run.Exec(root, tarantool, args)
}

// anyProjectRoot reports dir, the project projectRootDir found, as the root
// of a project of any kind: one with app.manifest.toml, or one with the pair
// of a template only, as tt create single_instance makes, which the instance
// commands work on just as well. tt run needs no package, only the
// tarantool of the project. A dir with no marker is the working directory
// outside every project.
func anyProjectRoot(dir string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", dir, err)
	}

	found, err := scope.HasProjectMarker(root)
	if err != nil {
		return "", fmt.Errorf("looking for the project in %s: %w", root, err)
	}

	if !found {
		return "", fmt.Errorf("%w: no %s, %s/*/%s or %s/*/%s in %s or its parents",
			app.ErrNoProject, scope.ManifestFileName, scope.TemplatesDirName,
			scope.InstancesFileName, scope.TemplatesDirName, scope.ConfigFileName, root)
	}

	return root, nil
}

// selectProjectTarantool resolves the root of the package the working
// directory is in (projectRootDir) and the interpreter it runs under, for
// tt test, which builds the package and so needs its manifest. tt run takes
// a project of any kind (anyProjectRoot).
func selectProjectTarantool() (string, string, error) {
	projectDir, err := projectRootDir()
	if err != nil {
		return "", "", err
	}

	root, err := run.ProjectRoot(projectDir)
	if err != nil {
		// A project of templates alone is still one tt run and the instance
		// commands work on: what it lacks is the package to build.
		return "", "", fmt.Errorf("%w; tt test builds the package, and tt new writes "+
			"a manifest for it", err)
	}

	tarantool, err := projectTarantool(root)
	if err != nil {
		return "", "", err
	}

	return root, tarantool, nil
}

// projectTarantool chooses the interpreter of the project at root, shared by
// tt run and tt test.
//
// The interpreter is chosen by the rule the instance commands follow for a
// project (cli/tarantool), so a package runs on the same tarantool under
// tt run as under tt instance start. bin_dir of tt.yaml plays no part in it.
// What the choice warns about, a project tarantool other than the one the
// lock bundles say, goes to the log.
func projectTarantool(root string) (string, error) {
	exe, err := run.Tarantool(root)
	if err != nil {
		return "", err
	}

	for _, warning := range exe.Warnings {
		log.Warn(warning)
	}

	return exe.Path, nil
}
