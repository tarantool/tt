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
//
// Cluster configurations come from [WithClusterConfig] rather than from
// files or storages. The project reads, edits and installs no file: its
// manifest is the one [WithManifest] and [WithDeclared] give, which
// AddDependency and AddComponent declare into, and they, Install,
// PrepareEnvironment and the Index of the rock servers record their calls
// ([Services.AddedDependencies], [Services.AddedComponents],
// [Services.Installs], [Services.PreparedEnvironments], [Services.Indexed])
// and answer as [WithMoves], [WithAddDependencyError], [WithResolveError],
// [WithInstall] and [WithIndex] say. [Services.Exit] does not end the test
// binary: under [Services.Run] it ends the command, and the [Result] says
// so.
package sdktest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/internal/clusteryaml"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/sdk/scope"
)

var (
	// errNoManifest reports the manifest of a project WithNoManifest made.
	errNoManifest = fmt.Errorf("no app.manifest.toml in the project: %w", sdk.ErrNotFound)
	// errComponent reports a component the fake does not declare.
	errComponent = errors.New("cannot declare the component")
)

// defaultName is the module name a Services logs under unless WithName says
// otherwise.
const defaultName = "test"

// anyVersion is the constraint of a dependency declared with none, as the
// core declares one.
const anyVersion = "*"

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
	clusters  map[sdk.ClusterSource]string
	dirs      map[sdk.ClusterSource]string
	declared  []sdk.Dependency
	moves     []sdk.DependencyMove
	addErr    error
	install   func(dir string, opts sdk.InstallOptions) error
	root      string
	manifest  *sdk.Manifest
	noMan     bool
	resolve   error
	index     func(dir string, opts sdk.IndexOptions) error
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

// WithProject sets the directory of the command, Project.Dir, made
// absolute; a new temporary directory by default. It is the project Root
// too, unless WithRoot says otherwise.
func WithProject(dir string) Option {
	return func(o *options) { o.project = dir }
}

// WithRoot sets the project the command works on, Project.Root, made
// absolute, for a command run below it: Dir is in it, or WithProject sets
// one that is. The directory of the command by default.
func WithRoot(dir string) Option {
	return func(o *options) { o.root = dir }
}

// WithManifest makes the manifest of the project declare what manifest
// does, the dependencies WithDeclared gives added; an empty manifest by
// default. Project.Manifest returns a copy of it with what AddDependency
// and AddComponent declared since, and AddDependency of a name it declares
// reports Existed and its version as Previous.
func WithManifest(manifest sdk.Manifest) Option {
	return func(o *options) { o.manifest = &manifest }
}

// WithNoManifest makes a project without app.manifest.toml: Manifest and
// AddComponent fail with an error wrapping sdk.ErrNotFound, and the
// component is not declared. It cannot be combined with WithManifest or
// WithDeclared.
func WithNoManifest() Option {
	return func(o *options) { o.noMan = true }
}

// WithResolveError makes every Project.AddComponent that declares its
// component fail with err afterwards, as a resolution of the lock that
// fails does: the component stays declared, and the change reports Declared
// with the error.
func WithResolveError(err error) Option {
	return func(o *options) { o.resolve = err }
}

// WithIndex makes Rocks().Index call index with the directory and the
// options it was given, and return what index returns: a module's test
// checks there what the module packed, or fails the index. Without it Index
// does nothing and succeeds; it records the call either way.
func WithIndex(index func(dir string, opts sdk.IndexOptions) error) Option {
	return func(o *options) { o.index = index }
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

// WithClusterConfig makes ClusterConfig return the cluster configuration
// yaml for src: built with the Tarantool hierarchy, as the core builds one,
// so sdk.Instances and sdk.InstanceConfig read it. No file, environment or
// storage is consulted. A source given no configuration is not found:
// ClusterConfig returns an error wrapping sdk.ErrNotFound. Sources match by
// equality, so the test and the module must build the same source - the
// same constructor with the same arguments, credentials included. YAML that
// does not parse fails the test in New.
//
// The Dir of the configuration is what the core reports: the absolute
// directory of the file for a FileSource, "" for any other source. Set it
// for an application with [WithClusterConfigDir].
func WithClusterConfig(src sdk.ClusterSource, yaml string) Option {
	return func(o *options) { o.clusters[src] = yaml }
}

// WithClusterConfigDir sets the Dir ClusterConfig reports for src, the
// directory relative paths in its configuration are relative to - for an
// application, the directory of its cluster config file.
func WithClusterConfigDir(src sdk.ClusterSource, dir string) Option {
	return func(o *options) { o.dirs[src] = dir }
}

// WithDeclared makes the manifest of the project declare deps already, each
// in the table its Dev names, at its Constraint ("*" for none) and with its
// Registry, over what WithManifest declares: Project.AddDependency of one of
// them to that table reports Existed and the constraint as Previous.
// Registries of deps are ignored.
func WithDeclared(deps ...sdk.Dependency) Option {
	return func(o *options) { o.declared = append(o.declared, deps...) }
}

// WithMoves sets the moves of the lock every successful
// Project.AddDependency reports; none by default, since the fake resolves
// nothing.
func WithMoves(moves ...sdk.DependencyMove) Option {
	return func(o *options) { o.moves = append(o.moves, moves...) }
}

// WithAddDependencyError makes every Project.AddDependency fail with err,
// declaring nothing; the call is recorded all the same.
func WithAddDependencyError(err error) Option {
	return func(o *options) { o.addErr = err }
}

// WithInstall makes Project.Install call install with the project root
// (WithRoot) and the options it was given, and return what install returns:
// a module's test writes there what tt package fetch would install, such as
// a program in .rocks/bin, or fails the install. Without it Install does
// nothing and succeeds; it records the call either way.
func WithInstall(install func(dir string, opts sdk.InstallOptions) error) Option {
	return func(o *options) { o.install = install }
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

	// mu guards answers, manifest and the records of the calls.
	mu      sync.Mutex
	answers []bool
	// manifest is what the manifest of the project declares.
	manifest sdk.Manifest
	// added are the calls of Project.AddDependency, in order.
	added []sdk.Dependency
	// components are the calls of Project.AddComponent, in order.
	components []sdk.Component
	// installs are the calls of Project.Install, in order.
	installs []sdk.InstallOptions
	// environments counts the calls of Project.PrepareEnvironment.
	environments int
	// indexed are the calls of Rocks().Index, in order.
	indexed []Index
}

// Index is a call of Rocks().Index ([Services.Indexed]).
type Index struct {
	// Dir is the directory to index.
	Dir string
	// Options are the options it was given.
	Options sdk.IndexOptions
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
		clusters:  map[sdk.ClusterSource]string{},
		dirs:      map[sdk.ClusterSource]string{},
		declared:  nil,
		moves:     nil,
		addErr:    nil,
		install:   nil,
		root:      "",
		manifest:  nil,
		noMan:     false,
		resolve:   nil,
		index:     nil,
	}

	for _, opt := range opts {
		opt(&config)
	}

	if config.noMan && (config.manifest != nil || len(config.declared) > 0) {
		tb.Fatalf("sdktest: WithNoManifest excludes WithManifest and WithDeclared")
	}

	manifest := copyManifest(config.manifest)
	for _, dep := range config.declared {
		tableOf(manifest, dep.Dev)[dep.Name] = declare(dep)
	}

	if config.answered && config.noPrompt {
		tb.Fatalf("sdktest: WithAnswers and WithNoPrompt exclude each other")
	}

	for src, data := range config.clusters {
		_, err := clusteryaml.Build(context.Background(), []byte(data))
		if err != nil {
			tb.Fatalf("sdktest: WithClusterConfig(%s): %v", src, err)
		}
	}

	if config.project == "" {
		config.project = tb.TempDir()
	}

	project, err := filepath.Abs(config.project)
	if err != nil {
		tb.Fatalf("sdktest: project directory %q: %v", config.project, err)
	}

	config.project = project

	if config.root == "" {
		config.root = project
	}

	root, err := filepath.Abs(config.root)
	if err != nil {
		tb.Fatalf("sdktest: project root %q: %v", config.root, err)
	}

	config.root = root

	logger, recorder := logtest.New(tb)

	return &Services{
		tb:           tb,
		opts:         config,
		started:      atomic.Bool{},
		logger:       logger.With("module", config.name),
		recorder:     recorder,
		stdout:       lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
		stderr:       lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
		mu:           sync.Mutex{},
		answers:      config.answers,
		manifest:     manifest,
		added:        nil,
		components:   nil,
		installs:     nil,
		environments: 0,
		indexed:      nil,
	}
}

// declare returns dep as the manifest declares it: at "*" when it names no
// constraint, from its registry.
func declare(dep sdk.Dependency) sdk.ManifestDependency {
	constraint := dep.Constraint
	if constraint == "" {
		constraint = anyVersion
	}

	return sdk.ManifestDependency{Version: constraint, Registry: dep.Registry, Path: ""}
}

// copyManifest returns a copy of manifest whose maps are its own and never
// nil; an empty manifest for nil.
func copyManifest(manifest *sdk.Manifest) sdk.Manifest {
	out := sdk.Manifest{
		Dependencies:    map[string]sdk.ManifestDependency{},
		DevDependencies: map[string]sdk.ManifestDependency{},
		Components:      map[string]sdk.ManifestComponent{},
	}

	if manifest == nil {
		return out
	}

	maps.Copy(out.Dependencies, manifest.Dependencies)
	maps.Copy(out.DevDependencies, manifest.DevDependencies)

	for name, component := range manifest.Components {
		if component.Build != nil {
			build := *component.Build

			component.Build = &build
		}

		out.Components[name] = component
	}

	return out
}

// tableOf returns the table of manifest a dependency goes to:
// DevDependencies with dev, Dependencies otherwise.
func tableOf(manifest sdk.Manifest, dev bool) map[string]sdk.ManifestDependency {
	if dev {
		return manifest.DevDependencies
	}

	return manifest.Dependencies
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

// Project returns the project WithProject and WithRoot set. It touches no
// file: its manifest is what WithManifest and WithDeclared give, and
// AddDependency and AddComponent declare into it; they, Install and
// PrepareEnvironment record the calls, which AddedDependencies,
// AddedComponents, Installs and PreparedEnvironments return, and do what
// WithMoves, WithAddDependencyError, WithNoManifest, WithResolveError and
// WithInstall say. StateDir is .tarantool of the root.
func (s *Services) Project() sdk.Project {
	s.ready("Project")

	return fakeProject{services: s}
}

// Rocks returns rock servers that write nothing: Index records its calls,
// which Indexed returns, and runs what WithIndex gave.
func (s *Services) Rocks() sdk.Rocks {
	s.ready("Rocks")

	return fakeRocks{services: s}
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

// ClusterConfig returns the configuration WithClusterConfig gave src, built
// afresh at every call, with the Dir WithClusterConfigDir set or the core's
// default for src, or an error wrapping sdk.ErrNotFound.
func (s *Services) ClusterConfig(
	ctx context.Context, src sdk.ClusterSource,
) (sdk.ClusterConfig, error) {
	s.ready("ClusterConfig")

	data, ok := s.opts.clusters[src]
	if !ok {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration of %s: %w",
			src, sdk.ErrNotFound)
	}

	cfg, err := clusteryaml.Build(ctx, []byte(data))
	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration of %s: %w", src, err)
	}

	dir, err := s.clusterDir(src)
	if err != nil {
		return sdk.ClusterConfig{}, err
	}

	return sdk.ClusterConfig{Config: cfg, Dir: dir}, nil
}

// Exit ends the command rather than the process: it panics with a value
// that [Services.Run] recovers and reports as a Result with Exited set and
// Err the error given. It must be called on the goroutine that runs the
// command, and under Run: anywhere else the panic is not recovered and
// fails the test, its value naming the error.
func (s *Services) Exit(err error) {
	s.ready("Exit")

	panic(&exitError{err: err})
}

// exitError is what Exit panics with.
type exitError struct {
	err error
}

// Error says that the command exited, for a panic Run does not recover.
func (e *exitError) Error() string {
	if e.err == nil {
		return "sdktest: the command called Services.Exit(nil) outside Services.Run"
	}

	return "sdktest: the command called Services.Exit outside Services.Run: " + e.err.Error()
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

// AddedDependencies returns the dependencies Project.AddDependency was given
// so far, in order, the failed calls included.
func (s *Services) AddedDependencies() []sdk.Dependency {
	s.mu.Lock()
	defer s.mu.Unlock()

	added := make([]sdk.Dependency, 0, len(s.added))
	for _, dep := range s.added {
		dep.Registries = slices.Clone(dep.Registries)
		added = append(added, dep)
	}

	return added
}

// Installs returns the options Project.Install was given so far, in order,
// the failed calls included.
func (s *Services) Installs() []sdk.InstallOptions {
	s.mu.Lock()
	defer s.mu.Unlock()

	installs := make([]sdk.InstallOptions, 0, len(s.installs))
	for _, opts := range s.installs {
		opts.Registries = slices.Clone(opts.Registries)
		installs = append(installs, opts)
	}

	return installs
}

// AddedComponents returns the components Project.AddComponent was given so
// far, in order, the failed calls included.
func (s *Services) AddedComponents() []sdk.Component {
	s.mu.Lock()
	defer s.mu.Unlock()

	added := make([]sdk.Component, 0, len(s.components))
	for _, component := range s.components {
		added = append(added, cloneComponent(component))
	}

	return added
}

// PreparedEnvironments returns how many times Project.PrepareEnvironment was
// called so far.
func (s *Services) PreparedEnvironments() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.environments
}

// Indexed returns the calls of Rocks().Index so far, in order, the failed
// ones included.
func (s *Services) Indexed() []Index {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.indexed)
}

// addDependency records dep and declares it as the core would: a name the
// table declares already keeps its declaration when dep has a registry and
// takes dep's constraint when it has none.
func (s *Services) addDependency(dep sdk.Dependency) (sdk.DependencyChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dep.Registries = slices.Clone(dep.Registries)
	s.added = append(s.added, dep)

	if s.opts.addErr != nil {
		return sdk.DependencyChange{}, s.opts.addErr
	}

	change := sdk.DependencyChange{
		Existed:  false,
		Previous: "",
		Moves:    slices.Clone(s.opts.moves),
	}

	table := tableOf(s.manifest, dep.Dev)

	existing, ok := table[dep.Name]
	if !ok {
		table[dep.Name] = declare(dep)

		return change, nil
	}

	change.Existed = true
	change.Previous = existing.Version

	if dep.Registry == "" {
		existing.Version = declare(dep).Version
		table[dep.Name] = existing
	}

	return change, nil
}

// addComponent records component and declares it as the core would: a
// component of no name, path or backend, a name declared already and a
// project WithNoManifest made are refused, and WithResolveError fails it
// once it is declared.
func (s *Services) addComponent(component sdk.Component) (sdk.ComponentChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	component = cloneComponent(component)
	s.components = append(s.components, component)

	refused := sdk.ComponentChange{Declared: false, Moves: nil}

	switch _, declared := s.manifest.Components[component.Name]; {
	case s.opts.noMan:
		return refused, errNoManifest
	case component.Name == "" || component.Path == "":
		return refused, fmt.Errorf("%w %q: it needs a name and a path", errComponent,
			component.Name)
	case component.Build != nil && component.Build.Backend == "":
		return refused, fmt.Errorf("%w %q: its build block names no backend", errComponent,
			component.Name)
	case declared:
		return refused, fmt.Errorf("%w %q: the manifest declares it already", errComponent,
			component.Name)
	}

	declared := sdk.ManifestComponent{Path: component.Path, Build: nil}

	if component.Build != nil {
		declared.Build = &sdk.ManifestBuild{Backend: component.Build.Backend, Cwd: ""}

		for _, field := range component.Build.Fields {
			if cwd, ok := field.Value.(string); ok && field.Key == "cwd" {
				declared.Build.Cwd = cwd
			}
		}
	}

	s.manifest.Components[component.Name] = declared

	if s.opts.resolve != nil {
		return sdk.ComponentChange{Declared: true, Moves: nil}, s.opts.resolve
	}

	return sdk.ComponentChange{Declared: true, Moves: slices.Clone(s.opts.moves)}, nil
}

// cloneComponent returns a copy of component that shares no slice with it.
func cloneComponent(component sdk.Component) sdk.Component {
	component.Include = slices.Clone(component.Include)
	component.Registries = slices.Clone(component.Registries)

	if component.Build != nil {
		build := *component.Build

		build.Fields = slices.Clone(build.Fields)
		component.Build = &build
	}

	return component
}

// readManifest returns a copy of the manifest of the project, or the error
// of a project WithNoManifest made.
func (s *Services) readManifest() (sdk.Manifest, error) {
	if s.opts.noMan {
		return sdk.Manifest{}, errNoManifest
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return copyManifest(&s.manifest), nil
}

// prepareEnvironment counts a call of Project.PrepareEnvironment.
func (s *Services) prepareEnvironment() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.environments++
}

// indexRocks records an index of dir and runs what WithIndex gave.
func (s *Services) indexRocks(dir string, opts sdk.IndexOptions) error {
	s.mu.Lock()

	s.indexed = append(s.indexed, Index{Dir: dir, Options: opts})

	s.mu.Unlock()

	if s.opts.index == nil {
		return nil
	}

	return s.opts.index(dir, opts)
}

// install records opts and runs what WithInstall gave.
func (s *Services) install(opts sdk.InstallOptions) error {
	s.mu.Lock()

	opts.Registries = slices.Clone(opts.Registries)
	s.installs = append(s.installs, opts)

	s.mu.Unlock()

	if s.opts.install == nil {
		return nil
	}

	return s.opts.install(s.opts.root, opts)
}

// clusterDir returns the Dir to report for src: the one WithClusterConfigDir
// set, else the absolute directory of a file source's file, else "".
func (s *Services) clusterDir(src sdk.ClusterSource) (string, error) {
	if dir, ok := s.opts.dirs[src]; ok {
		return dir, nil
	}

	path, ok := src.File()
	if !ok {
		return "", nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cluster configuration file %q: %w", path, err)
	}

	return filepath.Dir(abs), nil
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
	services *Services
}

// Dir returns the directory of the command.
func (f fakeProject) Dir() (string, error) { return f.services.opts.project, nil }

// Root returns the project WithRoot set, the directory of the command by
// default.
func (f fakeProject) Root() (string, error) { return f.services.opts.root, nil }

// Manifest returns what the manifest of the project declares.
func (f fakeProject) Manifest() (sdk.Manifest, error) { return f.services.readManifest() }

// AddComponent records component and declares it in the manifest of the
// project.
func (f fakeProject) AddComponent(
	_ context.Context, component sdk.Component,
) (sdk.ComponentChange, error) {
	change, err := f.services.addComponent(component)

	// The core calls it between the write and the resolution: the fake
	// writes and resolves nothing, so once declared, before the error
	// WithResolveError gives.
	if change.Declared && component.OnDeclared != nil {
		component.OnDeclared()
	}

	return change, err
}

// StateDir returns .tarantool of the root.
func (f fakeProject) StateDir() (string, error) {
	return filepath.Join(f.services.opts.root, scope.ProjectDirName), nil
}

// PrepareEnvironment records the call and writes nothing.
func (f fakeProject) PrepareEnvironment() error {
	f.services.prepareEnvironment()

	return nil
}

// fakeRocks are the rock servers of a Services.
type fakeRocks struct {
	services *Services
}

// Index records the call and runs what WithIndex gave.
func (f fakeRocks) Index(_ context.Context, dir string, opts sdk.IndexOptions) error {
	return f.services.indexRocks(dir, opts)
}

// AddDependency records dep and reports what WithDeclared and WithMoves
// make of it, or the error WithAddDependencyError gave.
func (f fakeProject) AddDependency(
	_ context.Context, dep sdk.Dependency,
) (sdk.DependencyChange, error) {
	return f.services.addDependency(dep)
}

// Install records opts and runs what WithInstall gave.
func (f fakeProject) Install(_ context.Context, opts sdk.InstallOptions) error {
	return f.services.install(opts)
}

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
