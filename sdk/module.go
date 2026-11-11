package sdk

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk/output"
)

// Mount is a command a module contributes to tt, and where it goes.
type Mount struct {
	// Path is the command's parent, named by command names from the root down
	// and separated by spaces: "" is the root, "replicaset vshard" is the
	// vshard group under replicaset. Aliases do not name a parent. A group on
	// the path that no module provides is created, without a description.
	Path string
	// Cmd is the command. It is fresh - built by the constructor that returns
	// it, with no parent - and the core owns it once the constructor returns:
	// the core adds it to the tree and wraps its hooks to report the errors
	// they return.
	Cmd *cobra.Command
}

// Constructor builds a module's commands. The core calls it once per
// process, before tt is configured, so only [Services.Log] may be used while
// it runs; the other services are for the commands' hooks, which run after.
// A constructor must not change process state: it declares commands and
// their flags and nothing else.
type Constructor func(Services) []Mount

// ErrNotFound reports a thing tt looked for and did not find, such as a
// Tarantool executable. Services wrap it; test for it with errors.Is.
var ErrNotFound = errors.New("not found")

// Services are what the tt core offers a module.
//
// Only the core and [github.com/tarantool/tt/sdk/sdktest] implement Services;
// a module must not, since methods may be added in a minor release of the
// SDK. Log is valid at any time. Every other method is valid only once tt is
// configured - in a command's hooks - and panics before that with a message
// naming the module and the method. Services are safe for concurrent use.
type Services interface {
	// Log returns the module's logger: the process logger with a
	// module=<name> attribute. Whether debug records are shown is the
	// process's --verbose; test it with Logger.Enabled.
	Log() *slog.Logger
	// Tarantool returns the Tarantool the command should run: the one the
	// project bundles, when it has one, and the environment's otherwise. The
	// project is the one [Project.Dir] is in, as
	// [github.com/tarantool/tt/sdk/scope.FindProject] finds it, so a command
	// run in a subdirectory of a project runs the project's Tarantool, as
	// tt run does. When there is none the error wraps ErrNotFound.
	Tarantool() (Tarantool, error)
	// Integrity returns the integrity checks files are read through.
	Integrity() Integrity
	// Project returns the project the command works on.
	Project() Project
	// Confirm asks the user question on stderr and reads a yes or no answer
	// from stdin. When the user asked tt not to prompt (--no-prompt) it asks
	// nothing and returns fallback, which is therefore the answer that is
	// safe to assume.
	Confirm(question string, fallback bool) (bool, error)
	// Streams returns the process's standard streams and the printers bound
	// to them.
	Streams() Streams
	// ClusterConfig returns the cluster configuration of src as Tarantool 3
	// sees it, as an immutable snapshot, with the directory of the file it
	// was read from ([ClusterConfig.Dir]):
	//
	//   - for an application or a file, the cluster config file merged with
	//     the TT_* environment and with the etcd or Tarantool config storage
	//     the file names, in Tarantool's order of precedence, the storage
	//     read through the integrity checks;
	//   - for a storage, what the storage holds under the URI's prefix (or
	//     at its key= parameter), and nothing else.
	//
	// The configuration is not validated against Tarantool's schema. It
	// carries the Tarantool hierarchy, so [Instances] and [InstanceConfig]
	// read it. An application or a file that does not exist, an application
	// without a cluster configuration and a storage that holds nothing under
	// the prefix (or at the key) wrap ErrNotFound. A storage that cannot be
	// reached is another error.
	ClusterConfig(ctx context.Context, src ClusterSource) (ClusterConfig, error)
	// Exit ends the process as the core ends it for a command that returned
	// err: it reports err once and exits with the code tt returns for it
	// ([ExitCode], and ExitSystem for a failure of the system). Exit(nil)
	// exits ExitOK. It is for code that cannot return an error to the
	// command, such as a callback of an interactive prompt; a command that
	// can return its error must. Exit does not return.
	Exit(err error)
}

// Tarantool is a Tarantool executable.
type Tarantool interface {
	// Path returns the path to the executable.
	Path() string
	// Version returns the version the executable reports. It runs the
	// executable the first time and remembers the answer.
	Version() (TarantoolVersion, error)
}

// Integrity reads files through tt's integrity checks: a file that fails
// them cannot be opened.
type Integrity interface {
	// Open opens the file at path for reading, having checked it.
	Open(path string) (io.ReadCloser, error)
}

// Project is the directory a command works on, and the package the
// app.manifest.toml in it describes, managed as tt package manages it.
//
// Only the core and [github.com/tarantool/tt/sdk/sdktest] implement Project,
// as they implement Services.
type Project interface {
	// Dir returns the project directory as an absolute path: the working
	// directory tt runs in. A module cannot declare -C; a command mounted
	// under a group of tt that declares -C for its subcommands, as tt package
	// does, gets the directory it names. No manifest is searched for: a
	// command that works on the project from any of its subdirectories, as
	// the project commands of tt do, finds it with
	// [github.com/tarantool/tt/sdk/scope.FindProject] from Dir.
	Dir() (string, error)
	// AddDependency declares dep in the app.manifest.toml of the project
	// directory and re-resolves app.manifest.lock, as tt package add does,
	// holding every version the lock already chose: only dep and what it
	// requires can move. Nothing is installed; [Project.Install] does that.
	//
	// When the table dep goes to declares the name already, the change
	// reports Existed and the constraint the declaration had. For a dep with
	// a Registry the declaration is left exactly as it is, constraint and
	// registry included, since they are someone's choice, and the lock is
	// resolved against it. For a dep without one its constraint is rewritten
	// to dep's, "*" for none, and the rest of it - a registry, a source - is
	// kept. A name declared in the other table as well stays there, with a
	// warning. The manifest is rewritten in place, every other line and
	// comment kept, and before the resolution: when the resolution fails the
	// declaration stays and the error says so.
	//
	// The warnings of the manifest and of the resolution are logged. A
	// project without a manifest, an invalid manifest or constraint, and a
	// closure no server can satisfy are errors; so is a tt with no Tarantool,
	// whose version the rockspecs are evaluated for, and that error wraps
	// ErrNotFound.
	AddDependency(ctx context.Context, dep Dependency) (DependencyChange, error)
	// Install materializes .rocks/ of the project strictly from its lock, as
	// tt package fetch does for the default product: the locked closure of
	// the product and the development closure are installed at the versions
	// the lock pins, other versions of those rocks are removed, and nothing
	// is resolved and no component of the project is built. A project
	// without a manifest or a lock, and a lock with no closure for the
	// product, are errors. A tt with no Tarantool installs the rocks that
	// need none, and fails on one compiled against the headers of
	// Tarantool. The warnings are logged.
	Install(ctx context.Context, opts InstallOptions) error
}

// Streams are the process's standard streams.
type Streams interface {
	// IO returns stdin, stdout and stderr.
	IO() output.Streams
	// Printer returns a Printer writing to stdout in format, with every
	// format the core encodes (such as YAML) and the core's check of whether
	// stdout is a terminal.
	Printer(format output.Format) (*output.Printer, error)
}
