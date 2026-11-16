// Package extmod runs tt's external modules: executables found in the
// directories TT_CLI_MODULES_PATH lists, each run as a top-level command of
// tt named after the directory that holds it.
//
// A module's directory holds a manifest (manifest.yaml) naming the module's
// executable, its version and its one-line help, or an executable named main
// that prints the version and the help when called with --description
// --version: YAML with version and help, or the help alone as plain text.
// The manifest is read, and the executable is read before each call, through
// tt's integrity checks: a module whose files they refuse is not run.
//
// Mount hangs a proxy command for every module at tt's root, by the name of
// its directory, without reading or running the module. The proxy takes its
// arguments as they are, flags included, runs the module with them on tt's
// standard streams and ends tt with the module's exit code. A module named
// like a command at the root takes the command's place, subcommands and all,
// unless -I keeps tt's own; only the debug log says that it does. A module
// with no command of its name is added. Mount also adds tt modules, which
// lists the modules.
//
// A module is asked to describe itself only for a listing of the commands:
// the help of tt, tt modules list and the completion of a command name. The
// reply is kept in the user cache (DefaultCacheDir) by the path, the size
// and the modification time of the executable, so a module is asked again
// once it changes. Only a listing warns about a module that cannot describe
// itself, and the completion does not.
package extmod

import (
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/core/internal/mount"
)

// PathEnv is the environment variable that lists the directories external
// modules are found in, separated by colons.
const PathEnv = "TT_CLI_MODULES_PATH"

// ttModule is the name the registry gives the module of the commands the
// core adds itself, such as tt modules.
const ttModule = "tt"

// protected are the commands no external module may take: the ones tt
// answers for itself whatever modules are installed - the list of the
// modules, the help, the completion scripts and the version - and the ones
// cobra completes the command line with.
var protected = []string{
	"modules", "help", "completion", "version",
	cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd,
}

// Opener opens a file for reading through tt's integrity checks: a file they
// refuse is not opened, or fails while it is read.
type Opener func(path string) (io.ReadCloser, error)

// Options are what Mount needs from tt.
type Options struct {
	// Path lists the directories to find modules in, separated by colons:
	// the value of PathEnv.
	Path string
	// Open reads the files of the modules through tt's integrity checks.
	Open Opener
	// ForceInternal keeps every command of tt in its place: -I.
	ForceInternal bool
	// Registry records which module each command of the tree comes from.
	Registry *mount.Registry
	// Reserved is what no module may take.
	Reserved mount.Reserved
	// Log receives the warnings about the modules and, at the debug level,
	// the commands they replace.
	Log *slog.Logger
	// Streams are the streams a module runs attached to and tt modules
	// lists the modules on.
	Streams output.Streams
	// CacheDir keeps the replies of the executables of the modules to
	// --description --version, DefaultCacheDir for tt; "" keeps none.
	CacheDir string
	// Args is the command line tt runs, without the name of tt: for a
	// completion of a command name, the modules it may list are asked to
	// describe themselves, so that it offers them with their help.
	Args []string
}

// Mount finds the external modules opts.Path lists and hangs a proxy for
// each at root, by its name, then adds tt modules. It returns the modules tt
// runs or would run without -I, sorted by name: the ones tt modules lists
// and the root help shows under EXTERNAL COMMANDS.
//
// Nothing of a module is read or run here: a module that cannot describe
// itself is found out by a listing (Module.Listed), and one that cannot run
// by its run. When opts.Args completes a command name, the modules whose
// names it may complete to describe themselves, quietly, for the completion
// to offer them with their help.
//
// A module is skipped, with a warning, when it is named like one of the
// protected commands, and when the tree cannot hold its command, such as
// when the name is another command's alias. Mount fails when a directory
// opts.Path lists is not one or cannot be read, and when tt modules cannot
// be added.
func Mount(root *cobra.Command, opts Options) ([]*Module, error) {
	found, err := discover(opts.Path, opts.Log)
	if err != nil {
		return nil, err
	}

	modules := make([]*Module, 0, len(found))

	for _, name := range slices.Sorted(maps.Keys(found)) {
		if slices.Contains(protected, name) {
			warnf(opts.Log, "External module %q (%s) is ignored: tt does not let a module "+
				"replace the command %q", name, found[name].Directory, name)

			continue
		}

		module := newModule(name, found[name], &opts)
		if hang(root, module, &opts) {
			modules = append(modules, module)
		}
	}

	err = opts.Registry.Replace(root, nil, newModulesCmd(modules, opts.Streams.Out), ttModule,
		opts.Reserved)
	if err != nil {
		return nil, fmt.Errorf("adding tt modules: %w", err)
	}

	prefix, completing := completedCommandName(root, opts.Args)
	if completing {
		for _, module := range modules {
			if strings.HasPrefix(module.Name, prefix) {
				_, _ = module.Describe()
			}
		}
	}

	return modules, nil
}

// completedCommandName returns the prefix of the command name args, the
// command line of tt, asks cobra to complete with descriptions, and false
// when args asks for no such completion: when it runs a command, when it
// completes the arguments or the flags of one, or when it asks for no
// descriptions.
func completedCommandName(root *cobra.Command, args []string) (string, bool) {
	if len(args) < 2 || args[0] != cobra.ShellCompRequestCmd {
		return "", false
	}

	toComplete := args[len(args)-1]
	if strings.HasPrefix(toComplete, "-") {
		return "", false
	}

	cmd, rest, err := root.Find(args[1 : len(args)-1])
	if err != nil || cmd != root {
		return "", false
	}

	for _, arg := range rest {
		if !strings.HasPrefix(arg, "-") {
			return "", false
		}
	}

	return toComplete, true
}

// hang puts the proxy of module at root, in place of the command of its
// name, if any, answering to that command's aliases too, and reports whether
// the module is available: hung, or kept out by opts.ForceInternal. A module
// the tree cannot hold is not, and is warned about. A replaced command is
// logged at the debug level only: whoever installed the module meant it to
// replace the command.
func hang(root *cobra.Command, module *Module, opts *Options) bool {
	old := byName(root, module.Name)
	if old != nil && opts.ForceInternal {
		return true
	}

	proxy := newProxy(module)
	replaced := ""

	if old != nil {
		replaced = fmt.Sprintf("command %q", old.Name())

		if owner, ok := opts.Registry.Owner(old); ok {
			replaced += fmt.Sprintf(" of module %q", owner)
		}

		// The module answers to the names the command did: with a module
		// named replicaset, tt rs runs the module, not an unknown command.
		proxy.Aliases = slices.Clone(old.Aliases)
	}

	err := opts.Registry.Replace(root, old, proxy, module.Name, opts.Reserved)
	if err != nil {
		warnf(opts.Log, "External module %q (%s) is ignored: %s", module.Name,
			module.location(), err)

		return false
	}

	module.proxy = proxy

	if old != nil {
		opts.Log.Debug(fmt.Sprintf("External module %q (%s) replaces the %s; "+
			"run tt with -I to keep it", module.Name, module.location(), replaced))
	}

	return true
}

// newProxy returns the command that runs module: it takes its arguments as
// they are, flags included, and its help is the module's. Its description
// is the module's one-line help once the module has described itself
// (Module.Describe), which the root help lists it with and the completion
// offers it with, as for any other command.
func newProxy(module *Module) *cobra.Command {
	proxy := &cobra.Command{
		Use:                module.Name,
		Short:              "",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			main, err := module.executable()
			if err != nil {
				return err
			}

			return runModule(main, args, module.opts.Open, module.opts.Streams)
		},
	}

	proxy.SetHelpFunc(helpFunc(module))

	return proxy
}

// helpFunc returns a help function that prints what module prints for
// --help, its executable read through the integrity checks first. The help
// goes to the command's output, stdout unless set otherwise, as any
// command's help does.
func helpFunc(module *Module) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, _ []string) {
		main, err := module.executable()
		if err != nil {
			cmd.PrintErrf("failed to get help for module %q: %s\n", module.Name, err)

			return
		}

		help, err := moduleHelp(main, module.opts.Open)
		if err != nil {
			cmd.PrintErrf("failed to get help for module %q: %s\n", module.Name, err)

			return
		}

		_, _ = fmt.Fprint(cmd.OutOrStdout(), help)
	}
}

// byName returns the command at root named name, or nil.
func byName(root *cobra.Command, name string) *cobra.Command {
	for _, cmd := range root.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}

	return nil
}

// warnf logs a formatted warning to logger.
func warnf(logger *slog.Logger, format string, args ...any) {
	logger.Warn(fmt.Sprintf(format, args...))
}
