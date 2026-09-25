// Package sdktest is a fake of the tt core for testing modules: [Services]
// that record what a module does with them, and [Run], which hangs a
// module's commands on a root the way the core does and runs one.
//
// Like the core, a new Services is not started: until [Services.Start] every
// method but Log panics, naming the module and the method, so a constructor
// that uses a service it cannot have yet fails in its test as it would in tt.
//
// What a module logs is recorded (see [Services.Records]) rather than written
// to stderr. Printers encode the human format and JSON; YAML, which the core
// encodes, is refused with output.ErrUnknownFormat, as by a Printer made
// without the core's encoders.
package sdktest

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// defaultName is the module name a Services logs under unless WithName says
// otherwise.
const defaultName = "test"

// options are what the Options given to New set.
type options struct {
	name      string
	tarantool *fakeTarantool
	project   string
	answers   []bool
	answered  bool
	noPrompt  bool
	stdin     io.Reader
	open      func(path string) (io.ReadCloser, error)
}

// Option configures the Services New returns.
type Option func(*options)

// WithName sets the module name the Services log under, as module=<name>;
// "test" by default.
func WithName(name string) Option {
	return func(o *options) { o.name = name }
}

// WithTarantool makes Tarantool return an executable at path reporting
// version. Without it Tarantool finds none and returns an error wrapping
// sdk.ErrNotFound.
func WithTarantool(path string, version sdk.TarantoolVersion) Option {
	return func(o *options) { o.tarantool = &fakeTarantool{path: path, version: version} }
}

// WithProject sets the project directory, made absolute; a new temporary
// directory by default.
func WithProject(dir string) Option {
	return func(o *options) { o.project = dir }
}

// WithAnswers gives the answers Confirm returns, in order. A Confirm with no
// answer left fails the test; so does any Confirm when WithAnswers is not
// given, unless WithNoPrompt is.
func WithAnswers(answers ...bool) Option {
	return func(o *options) {
		o.answers = append(o.answers, answers...)
		o.answered = true
	}
}

// WithNoPrompt is tt run with --no-prompt: Confirm asks nothing and returns
// its fallback. It cannot be combined with WithAnswers.
func WithNoPrompt() Option {
	return func(o *options) { o.noPrompt = true }
}

// WithStdin sets what the command reads from stdin; nothing by default.
// Confirm does not read it: its answers come from WithAnswers.
func WithStdin(stdin io.Reader) Option {
	return func(o *options) { o.stdin = stdin }
}

// WithIntegrity sets how Integrity opens files; os.Open by default, which
// checks nothing.
func WithIntegrity(open func(path string) (io.ReadCloser, error)) Option {
	return func(o *options) { o.open = open }
}

// Services is a fake of the Services the tt core gives a module. It is safe
// for concurrent use.
type Services struct {
	tb       testing.TB
	opts     options
	started  atomic.Bool
	logger   *slog.Logger
	recorder *logtest.Recorder
	stdout   lockedBuffer
	stderr   lockedBuffer

	// mu guards answers.
	mu      sync.Mutex
	answers []bool
}

var _ sdk.Services = (*Services)(nil)

// New returns Services for a module under test, not yet started.
func New(tb testing.TB, opts ...Option) *Services {
	tb.Helper()

	config := options{
		name:      defaultName,
		tarantool: nil,
		project:   "",
		answers:   nil,
		answered:  false,
		noPrompt:  false,
		stdin:     strings.NewReader(""),
		open:      openFile,
	}

	for _, opt := range opts {
		opt(&config)
	}

	if config.answered && config.noPrompt {
		tb.Fatalf("sdktest: WithAnswers and WithNoPrompt exclude each other")
	}

	if config.project == "" {
		config.project = tb.TempDir()
	}

	project, err := filepath.Abs(config.project)
	if err != nil {
		tb.Fatalf("sdktest: project directory %q: %v", config.project, err)
	}

	config.project = project

	logger, recorder := logtest.New(tb)

	return &Services{
		tb:       tb,
		opts:     config,
		started:  atomic.Bool{},
		logger:   logger.With("module", config.name),
		recorder: recorder,
		stdout:   lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
		stderr:   lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
		mu:       sync.Mutex{},
		answers:  config.answers,
	}
}

// openFile opens path with os.Open.
func openFile(path string) (io.ReadCloser, error) {
	return os.Open(path) //nolint:wrapcheck // The fake opens what it is asked to.
}

// Start marks tt configured: from now on every method may be used. Starting
// again does nothing.
func (s *Services) Start() {
	s.started.Store(true)
}

// Log returns the module's logger, which records into Records. It may be
// used before Start.
func (s *Services) Log() *slog.Logger {
	return s.logger
}

// Tarantool returns the executable WithTarantool set, or an error wrapping
// sdk.ErrNotFound.
func (s *Services) Tarantool() (sdk.Tarantool, error) {
	s.ready("Tarantool")

	if s.opts.tarantool == nil {
		return nil, fmt.Errorf("tarantool: %w", sdk.ErrNotFound)
	}

	return s.opts.tarantool, nil
}

// Integrity returns the checks WithIntegrity set.
func (s *Services) Integrity() sdk.Integrity {
	s.ready("Integrity")

	return fakeIntegrity{open: s.opts.open}
}

// Project returns the project WithProject set.
func (s *Services) Project() sdk.Project {
	s.ready("Project")

	return fakeProject{dir: s.opts.project}
}

// Confirm writes question to stderr the way tt prompts and returns the next
// answer WithAnswers gave, failing the test when there is none left. Under
// WithNoPrompt it writes nothing and returns fallback.
func (s *Services) Confirm(question string, fallback bool) (bool, error) {
	s.ready("Confirm")

	if s.opts.noPrompt {
		return fallback, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = fmt.Fprintf(&s.stderr, "%s [y/n]: ", question)

	if len(s.answers) == 0 {
		s.tb.Fatalf("sdktest: Confirm(%q): no answer left; give it with WithAnswers "+
			"or use WithNoPrompt", question)
	}

	answer := s.answers[0]

	s.answers = s.answers[1:]

	return answer, nil
}

// Streams returns streams reading WithStdin and writing to Stdout and
// Stderr.
func (s *Services) Streams() sdk.Streams {
	s.ready("Streams")

	return fakeStreams{services: s}
}

// Stdout returns what was written to stdout so far.
func (s *Services) Stdout() string {
	return s.stdout.String()
}

// Stderr returns what was written to stderr so far: prompts, and whatever a
// command wrote there itself. Log records are not in it; see Records.
func (s *Services) Stderr() string {
	return s.stderr.String()
}

// Records returns the records logged through Log so far, each carrying the
// module attribute.
func (s *Services) Records() []logtest.Record {
	return s.recorder.Records()
}

// ready panics, as the core does, when method is used before Start.
func (s *Services) ready(method string) {
	if !s.started.Load() {
		panic(fmt.Sprintf("module %q used Services.%s before tt was configured; "+
			"use it in a command's hooks, not in the constructor", s.opts.name, method))
	}
}

// io returns the fake's standard streams.
func (s *Services) io() output.Streams {
	return output.Streams{In: s.opts.stdin, Out: &s.stdout, Err: &s.stderr}
}

// fakeTarantool is the Tarantool WithTarantool describes.
type fakeTarantool struct {
	path    string
	version sdk.TarantoolVersion
}

// Path returns the path WithTarantool was given.
func (f *fakeTarantool) Path() string { return f.path }

// Version returns the version WithTarantool was given.
func (f *fakeTarantool) Version() (sdk.TarantoolVersion, error) { return f.version, nil }

// fakeIntegrity opens files with the function WithIntegrity was given.
type fakeIntegrity struct {
	open func(path string) (io.ReadCloser, error)
}

// Open opens path.
func (f fakeIntegrity) Open(path string) (io.ReadCloser, error) {
	return f.open(path)
}

// fakeProject is the project WithProject set.
type fakeProject struct {
	dir string
}

// Dir returns the project directory.
func (f fakeProject) Dir() (string, error) { return f.dir, nil }

// fakeStreams are a Services' streams.
type fakeStreams struct {
	services *Services
}

// IO returns the streams.
func (f fakeStreams) IO() output.Streams { return f.services.io() }

// Printer returns a Printer writing to the fake stdout in format: the human
// format or JSON.
func (f fakeStreams) Printer(format output.Format) (*output.Printer, error) {
	printer, err := output.NewPrinter(f.services.io(), format)
	if err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}

	return printer, nil
}

// lockedBuffer is a bytes.Buffer safe for concurrent use.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer.Write never fails.
}

// String returns what was written.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
