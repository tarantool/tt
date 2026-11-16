package cmd

import (
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/manifest/run"
)

// NewRunCmd creates `tt run`: the project's Tarantool, with the arguments
// passed straight through.
func NewRunCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run [TARANTOOL-OPTIONS...] [SCRIPT [ARGS...]]",
		Short: "Run the current package under its Tarantool",
		Long: `Run the Tarantool of the current project, in the current directory.

The project is the one the current directory is in: the nearest directory
holding app.manifest.toml, the current one or one above it. No tt environment
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
	root, tarantool, err := selectProjectTarantool()
	if err != nil {
		return err
	}

	return run.Exec(root, tarantool, args)
}

// selectProjectTarantool resolves the project root and the interpreter it runs
// under, shared by tt run and tt test. The root is the project the working
// directory is in (projectRootDir), so both work from any of its
// subdirectories: tt run in that subdirectory, tt test in the root.
//
// The interpreter is chosen by the rule the instance commands follow for a
// project (cli/tarantool), so a package runs on the same tarantool under
// tt run as under tt instance start. bin_dir of tt.yaml plays no part in it.
// What the choice warns about, a project tarantool other than the one the
// lock bundles say, goes to the log.
func selectProjectTarantool() (string, string, error) {
	projectDir, err := projectRootDir()
	if err != nil {
		return "", "", err
	}

	root, err := run.ProjectRoot(projectDir)
	if err != nil {
		return "", "", err
	}

	exe, err := run.Tarantool(root)
	if err != nil {
		return "", "", err
	}

	for _, warning := range exe.Warnings {
		log.Warn(warning)
	}

	return root, exe.Path, nil
}
