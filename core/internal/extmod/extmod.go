// Package extmod runs tt's external modules: executables found in the
// directories TT_CLI_MODULES_PATH lists, each run as a top-level command of
// tt named after the directory that holds it.
//
// A module's directory holds a manifest (manifest.yaml) naming the module's
// executable, its version and its one-line help, or an executable named main
// that prints the version and the help when called with --description
// --version. The manifest is read, and the executable is read before each
// call, through tt's integrity checks: a module whose files they refuse is
// not run.
//
// Mount hangs a proxy command for every module at tt's root. The proxy takes
// its arguments as they are, flags included, runs the module with them on
// tt's standard streams and ends tt with the module's exit code. A module
// named like a command at the root takes the command's place, subcommands
// and all, unless -I keeps tt's own; only the debug log says that it does. A
// module with no command of its name is added. Mount also adds tt modules,
// which lists the modules.
package extmod

import (
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"

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
}

// Mount finds the external modules opts.Path lists and hangs a proxy for
// each at root, then adds tt modules. It returns the modules tt runs or
// would run without -I, sorted by name: the ones tt modules lists and the
// help and the completion show.
//
// A module is skipped, with a warning, when it cannot be described - no
// manifest and no answer to --description --version, a manifest the
// integrity checks refuse or, for a module with no manifest, an executable
// they refuse, a manifest that does not parse -, when it is named like one
// of the protected commands, and when the tree cannot hold its command, such
// as when the name is another command's alias. A module with a manifest
// whose executable the checks refuse is kept: its runs and its help fail.
// Mount fails when a directory opts.Path lists is not one or cannot be read,
// and when tt modules cannot be added.
func Mount(root *cobra.Command, opts Options) ([]Manifest, error) {
	found, err := discover(opts.Path, opts.Log)
	if err != nil {
		return nil, err
	}

	modules := make([]Manifest, 0, len(found))

	for _, name := range slices.Sorted(maps.Keys(found)) {
		manifest, ok := describe(name, found[name], opts)
		if !ok {
			continue
		}

		if hang(root, manifest, opts) {
			modules = append(modules, manifest)
		}
	}

	err = opts.Registry.Replace(root, nil, newModulesCmd(modules, opts.Streams.Out), ttModule,
		opts.Reserved)
	if err != nil {
		return nil, fmt.Errorf("adding tt modules: %w", err)
	}

	return modules, nil
}

// describe returns the manifest of the module name found at entry, and
// false, having warned, for a module that is protected or cannot be
// described. A protected module is not read at all.
func describe(name string, entry modulesEntry, opts Options) (Manifest, bool) {
	var none Manifest

	if slices.Contains(protected, name) {
		warnf(opts.Log, "External module %q (%s) is ignored: tt does not let a module "+
			"replace the command %q", name, entry.Directory, name)

		return none, false
	}

	manifest, err := makeManifest(entry, opts.Open)
	if err != nil {
		warnf(opts.Log, "Failed to get information about module %q: %s", name, err)

		return none, false
	}

	manifest.Name = name

	return manifest, true
}

// hang puts the proxy of the module of manifest at root, in place of the
// command of its name, if any, answering to that command's aliases too, and
// reports whether the module is available: hung, or kept out by
// opts.ForceInternal. A module the tree cannot hold is not, and is warned
// about. A replaced command is logged at the debug level only: whoever
// installed the module meant it to replace the command.
func hang(root *cobra.Command, manifest Manifest, opts Options) bool {
	old := byName(root, manifest.Name)
	if old != nil && opts.ForceInternal {
		return true
	}

	proxy := newProxy(manifest, opts)
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

	err := opts.Registry.Replace(root, old, proxy, manifest.Name, opts.Reserved)
	if err != nil {
		warnf(opts.Log, "External module %q (%s) is ignored: %s", manifest.Name,
			manifest.Main, err)

		return false
	}

	if old != nil {
		opts.Log.Debug(fmt.Sprintf("External module %q (%s) replaces the %s; "+
			"run tt with -I to keep it", manifest.Name, manifest.Main, replaced))
	}

	return true
}

// newProxy returns the command that runs the module of manifest: it takes
// its arguments as they are, flags included, and its help is the module's.
func newProxy(manifest Manifest, opts Options) *cobra.Command {
	proxy := &cobra.Command{
		Use:                manifest.Name,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runModule(manifest.Main, args, opts.Open, opts.Streams)
		},
	}

	proxy.SetHelpFunc(helpFunc(manifest, opts.Open))

	return proxy
}

// helpFunc returns a help function that prints what the module of manifest
// prints for --help, its executable read through open first. The help goes
// to the command's output, stdout unless set otherwise, as any command's
// help does.
func helpFunc(manifest Manifest, open Opener) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, _ []string) {
		help, err := moduleHelp(manifest.Main, open)
		if err != nil {
			cmd.PrintErrf("failed to get help for module %q: %s\n", manifest.Name, err)

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
