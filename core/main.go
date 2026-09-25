// Package core builds tt from a table of modules and runs it.
//
// A module is an sdk.Constructor: given the Services the core offers, it
// returns the commands it contributes and where they go. Main calls every
// constructor, hangs the commands on tt's root, configures tt and runs the
// command line. A distribution of tt is a main package that calls Main with
// its table:
//
//	func main() {
//		os.Exit(core.Main(core.Modules{"builtin": core.Builtin}))
//	}
//
// Nothing under cli/ but tt's main package may import this package: the core
// is built on top of the commands, never the other way round.
package core

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync/atomic"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/core/internal/mount"
)

// Modules are the modules tt is built from, by name. The name is the one
// errors and logs refer to the module by.
type Modules map[string]sdk.Constructor

// Option configures Main.
type Option func(*options)

// options are what the Options given to Main set.
type options struct {
	// flavour is how tt presents itself.
	flavour version.Flavour
}

// Flavour is how a distribution of tt presents itself: its name and its
// version, in the help, in tt version and in the report of an internal
// error. The version the distribution's core is - the one [platform].tt
// constraints are checked against and the one written to the files tt
// generates - stays the core's.
type Flavour struct {
	// Title is the distribution's name: "Tarantool CLI EE". Empty means
	// tt's own, "Tarantool CLI"; any other title heads the root help.
	Title string
	// Version is the distribution's version. A zero Version is an unknown
	// one.
	Version VersionInfo
	// Edition, when set, follows the version tt version --short prints, as
	// semver build metadata: "ee" makes it 2.15.0+ee.
	Edition string
}

// VersionInfo describes what a build of a distribution was made from.
type VersionInfo struct {
	// Tag is the tag the build is at or descends from, as git describe
	// prints it: v2.15.0, or v2.15.0-3-gabc1234 for a commit past it. Only
	// its numeric part makes the version. Empty means the version is
	// unknown.
	Tag string
	// Commit is the abbreviated hash of the commit built.
	Commit string
	// CommitsSinceTag is the number of commits between Tag and Commit. When
	// it is not zero tt version shows Tag as well.
	CommitsSinceTag int
	// Label, when set, follows the version: 2.15.0/label.
	Label string
}

// WithFlavour makes tt present itself as the distribution flavour
// describes. Without it tt presents itself as tt.
func WithFlavour(flavour Flavour) Option {
	return func(o *options) {
		o.flavour = version.Flavour{
			Title: flavour.Title,
			Version: &version.Info{
				Tag:             flavour.Version.Tag,
				Commit:          flavour.Version.Commit,
				CommitsSinceTag: flavour.Version.CommitsSinceTag,
				Label:           flavour.Version.Label,
			},
			Edition: flavour.Edition,
		}
	}
}

// Main builds tt from modules, runs the command line in os.Args and returns
// the process exit code, having reported the error tt failed with. It may be
// called once per process.
//
// It boots tt - global flags, logging - and calls the constructors in the
// order of their names, then hangs their commands, adds the commands
// injected through cmd.InjectedCmds, configures tt and runs the command.
// Services other than Log are usable from the moment tt is configured. A
// constructor that panics, a mount the tree cannot hold, a failure to
// configure tt are reported and end tt with a failure before any command
// runs; a panic anywhere is reported as an internal error.
//
//nolint:nonamedreturns // The deferred recover sets the exit code.
func Main(modules Modules, opts ...Option) (code int) {
	var config options

	for _, opt := range opts {
		opt(&config)
	}

	defer func() {
		if r := recover(); r != nil {
			err := util.InternalError("Unhandled internal error: %s",
				config.flavour.GetVersion, r)
			exitcode.Report(err)

			code = exitcode.Code(err)
		}
	}()

	ready := &atomic.Bool{}

	_, err := build(os.Args[1:], modules, ready, config)
	if err != nil {
		exitcode.Report(err)

		return exitcode.Code(err)
	}

	ready.Store(true)

	return cmd.Run()
}

// build boots tt as config says with the command-line arguments args, hangs
// the commands of modules, whose Services are ready once ready is set, and
// configures tt. It returns the root, ready to run.
func build(
	args []string, modules Modules, ready *atomic.Bool, config options,
) (*cobra.Command, error) {
	root, err := cmd.Boot(cmd.BootOptions{Args: args, Flavour: config.flavour})
	if err != nil {
		return nil, err
	}

	entries, err := construct(modules, ready, config.flavour.GetVersion)
	if err != nil {
		return nil, err
	}

	registry, err := mount.Hang(root, entries, reserved())
	if err != nil {
		return nil, err
	}

	err = cmd.InjectCommands(root)
	if err != nil {
		return nil, err
	}

	err = cmd.Configure(cmd.ConfigureOptions{ModuleOwner: registry.Owner})
	if err != nil {
		return nil, err
	}

	return root, nil
}

// construct calls the constructor of every module, in the order of their
// names, and returns their mounts. A constructor that panics is an internal
// error naming its module and the version getVersion reports; the other
// constructors are still called, so that every such error is reported at
// once.
func construct(
	modules Modules, ready *atomic.Bool, getVersion util.VersionFunc,
) ([]mount.Entry, error) {
	var (
		entries []mount.Entry
		errs    []error
	)

	for _, name := range slices.Sorted(maps.Keys(modules)) {
		mounts, err := callConstructor(name, modules[name], newServices(name, ready),
			getVersion)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, m := range mounts {
			entries = append(entries, mount.Entry{
				Module: name,
				Path:   m.Path,
				Cmd:    m.Cmd,
				Legacy: m.Cmd != nil && m.Cmd.Annotations[cmd.LegacyAnnotation] == "true",
			})
		}
	}

	return entries, errors.Join(errs...)
}

// errNoConstructor reports a module listed without a constructor.
var errNoConstructor = errors.New("no constructor")

// callConstructor calls ctor, the constructor of the module name, turning a
// panic into an internal error naming the module and the version getVersion
// reports.
func callConstructor(
	name string, ctor sdk.Constructor, services sdk.Services, getVersion util.VersionFunc,
) (_ []sdk.Mount, err error) {
	if ctor == nil {
		return nil, fmt.Errorf("module %q: %w", name, errNoConstructor)
	}

	defer func() {
		if r := recover(); r != nil {
			err = util.InternalError("module %q panicked while building its commands: %v",
				getVersion, name, r)
		}
	}()

	return ctor(services), nil
}

// reserved returns what no module may take: the command names cobra uses at
// the root, and the flags every command answers to (-h) or that tt reserves
// for itself (-C).
func reserved() mount.Reserved {
	flags := pflag.NewFlagSet("reserved", pflag.ContinueOnError)
	flags.BoolP("help", "h", false, "")
	flags.StringP("directory", "C", "", "")

	return mount.Reserved{
		Names: []string{"help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd},
		Flags: flags,
	}
}

// Builtin is the module of tt's own commands: the commands tt had before it
// was built from modules, each mounted at the root as it is.
func Builtin(sdk.Services) []sdk.Mount {
	commands := cmd.BuiltinCommands()
	mounts := make([]sdk.Mount, 0, len(commands))

	for _, command := range commands {
		mounts = append(mounts, sdk.Mount{Path: "", Cmd: command})
	}

	return mounts
}
