package sdk

import (
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
	// project bundles, when it has one, and the environment's otherwise.
	// When there is none the error wraps ErrNotFound.
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

// Project is the directory a command works on.
type Project interface {
	// Dir returns the project directory as an absolute path: the directory
	// the user named on the command line, or the working directory. No
	// manifest is searched for.
	Dir() (string, error)
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
