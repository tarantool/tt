package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/manifest/pack"
	"github.com/tarantool/tt/v3/cli/manifest/run"
	"github.com/tarantool/tt/v3/cli/printing"
)

// moduleKey is the attribute key that names the module a record came from.
const moduleKey = "module"

// services are the Services of one module.
type services struct {
	// module is the module's name.
	module string
	// ready is set once tt is configured.
	ready *atomic.Bool
	// streams are the process's standard streams.
	streams output.Streams
}

var _ sdk.Services = (*services)(nil)

// newServices returns the Services of the module name, usable once ready is
// set.
func newServices(name string, ready *atomic.Bool) *services {
	return &services{module: name, ready: ready, streams: output.StdStreams()}
}

// Log returns the process logger with the module attribute. It is taken
// from the process logger at every call, so it follows the logger tt sets
// up, and draws a spinner (log.SpinnerOf) wherever the process logger does.
func (s *services) Log() *slog.Logger {
	return log.Logger().With(moduleKey, s.module)
}

// Tarantool returns the Tarantool the project bundles under _runtime/ when
// it has one, and otherwise the one the tt environment resolved or the first
// on PATH. TT_USE_SYSTEM_TARANTOOL puts the bundled one last.
func (s *services) Tarantool() (sdk.Tarantool, error) {
	s.mustBeReady("Tarantool")

	dir, err := cmd.ProjectDir()
	if err != nil {
		return nil, err
	}

	path, err := run.SelectTarantool(dir, run.Environment{
		Executable: cmd.GetCmdCtxPtr().Cli.TarantoolCli.Executable,
		UseSystem:  run.UseSystemFromEnv(),
	})
	if errors.Is(err, run.ErrNoTarantool) {
		return nil, notFoundError{err: err}
	}

	if err != nil {
		return nil, err
	}

	// Only the path is known here: Version fills the rest on first use.
	found := new(tarantool)

	found.path = path

	return found, nil
}

// Integrity returns the integrity checks tt was configured with.
func (s *services) Integrity() sdk.Integrity {
	s.mustBeReady("Integrity")

	return integrityChecks{}
}

// Project returns the project the command line names with -C, or the
// working directory.
func (s *services) Project() sdk.Project {
	s.mustBeReady("Project")

	return project{}
}

// Confirm asks question on stderr, in the form tt's prompts take, until the
// answer read from stdin is yes or no. Under --no-prompt it asks nothing and
// returns fallback.
func (s *services) Confirm(question string, fallback bool) (bool, error) {
	s.mustBeReady("Confirm")

	if cmd.GetCmdCtxPtr().Cli.NoPrompt {
		return fallback, nil
	}

	for {
		_, _ = fmt.Fprintf(s.streams.Err, "%s [y/n]: ", question)

		line, err := readLine(s.streams.In)

		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}

		if err != nil {
			return false, fmt.Errorf("reading the answer to %q: %w", question, err)
		}
	}
}

// Streams returns the process's standard streams.
func (s *services) Streams() sdk.Streams {
	s.mustBeReady("Streams")

	return streams{io: s.streams}
}

// mustBeReady panics when method is used before tt is configured.
func (s *services) mustBeReady(method string) {
	if !s.ready.Load() {
		panic(fmt.Sprintf("module %q used Services.%s before tt was configured; "+
			"use it in a command's hooks, not in the constructor", s.module, method))
	}
}

// readLine reads from r up to and including the next line break, a byte at
// a time so that nothing after the line is consumed. It returns what it
// read, without the line break, and the error that stopped it.
func readLine(reader io.Reader) (string, error) {
	var (
		line strings.Builder
		char [1]byte
	)

	for {
		n, err := reader.Read(char[:])
		if n == 1 {
			if char[0] == '\n' {
				return line.String(), nil
			}

			line.WriteByte(char[0])
		}

		if err != nil {
			return line.String(), err
		}
	}
}

// notFoundError is a failure to find something tt looked for: it reads as
// the error it wraps and is sdk.ErrNotFound.
type notFoundError struct {
	err error
}

// Error renders the wrapped error.
func (e notFoundError) Error() string { return e.err.Error() }

// Unwrap exposes the wrapped error.
func (e notFoundError) Unwrap() error { return e.err }

// Is reports whether target is sdk.ErrNotFound.
func (notFoundError) Is(target error) bool { return target == sdk.ErrNotFound }

// tarantool is a Tarantool executable.
type tarantool struct {
	path string

	once    sync.Once
	version sdk.TarantoolVersion
	err     error
}

// Path returns the path to the executable.
func (t *tarantool) Path() string { return t.path }

// Version runs the executable with --version the first time and returns the
// version and edition its banner reports.
func (t *tarantool) Version() (sdk.TarantoolVersion, error) {
	t.once.Do(func() {
		t.version, t.err = tarantoolVersion(t.path)
	})

	return t.version, t.err
}

// errNoVersion reports a --version banner with no version in it.
var errNoVersion = errors.New("no version in the output of --version")

// tarantoolVersion runs the Tarantool at path with --version and parses its
// banner: the version is the last word of the first line, the edition is
// told by the banner's wording.
func tarantoolVersion(path string) (sdk.TarantoolVersion, error) {
	out, err := exec.CommandContext(context.Background(), path, "--version").Output()
	if err != nil {
		return sdk.TarantoolVersion{}, fmt.Errorf("getting the version of %s: %w", path, err)
	}

	banner := string(out)
	first, _, _ := strings.Cut(banner, "\n")

	fields := strings.Fields(first)
	if len(fields) < 2 { //nolint:mnd // "Tarantool <version>" at least.
		return sdk.TarantoolVersion{}, fmt.Errorf("%s: %w: %q", path, errNoVersion, first)
	}

	version, err := sdk.ParseTarantoolVersion(fields[len(fields)-1])
	if err != nil {
		return sdk.TarantoolVersion{}, fmt.Errorf("getting the version of %s: %w", path, err)
	}

	switch pack.FlavorFromBanner(banner) {
	case "ce":
		version.Edition = sdk.EditionCE
	case "ee":
		version.Edition = sdk.EditionEE
	default:
		version.Edition = sdk.EditionUnknown
	}

	return version, nil
}

// integrityChecks read files through the integrity repository tt was
// configured with.
type integrityChecks struct{}

// Open opens path through the integrity repository.
func (integrityChecks) Open(path string) (io.ReadCloser, error) {
	return cmd.GetCmdCtxPtr().Integrity.Repository.Read(path)
}

// project is the project of the command line.
type project struct{}

// Dir returns cmd.ProjectDir.
func (project) Dir() (string, error) {
	return cmd.ProjectDir()
}

// streams are the process's standard streams.
type streams struct {
	io output.Streams
}

// IO returns the streams.
func (s streams) IO() output.Streams { return s.io }

// Printer returns a Printer writing to stdout in format, with the formats
// and the terminal check of tt's own commands.
func (s streams) Printer(format output.Format) (*output.Printer, error) {
	printer, err := output.NewPrinter(s.io, format, printing.Options()...)
	if err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}

	return printer, nil
}
