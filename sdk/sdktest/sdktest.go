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
// files or storages. The project reads, edits, builds and installs no file:
// its manifest is the one [WithManifest] and [WithDeclared] give, which
// AddDependency, RemoveDependency, AddComponent, UpdateComponent and
// RemoveComponent change as the core would, and they, Resolve, Build, Clean,
// Install, PrepareEnvironment and the Index of the rock servers record their
// calls ([Services.AddedDependencies], [Services.RemovedDependencies],
// [Services.AddedComponents], [Services.UpdatedComponents],
// [Services.RemovedComponents], [Services.Resolves], [Services.Builds],
// [Services.Cleans], [Services.Installs], [Services.PreparedEnvironments],
// [Services.Indexed]) and answer as [WithMoves], [WithAddDependencyError],
// [WithResolveError], [WithLock], [WithBuild], [WithBuildContext],
// [WithClean], [WithInstall] and [WithIndex] say. Project.Lock waits for
// nothing; [Services.Locks] counts the locks it took and those not
// released. [Services.Exit] does not end the test binary: under
// [Services.Run] it ends the command, and the [Result] says so.
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
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/sdk/v3/internal/clusteryaml"
	"github.com/tarantool/tt/sdk/v3/log/logtest"
	"github.com/tarantool/tt/sdk/v3/output"
	"github.com/tarantool/tt/sdk/v3/scope"
)

var (
	// errNoManifest reports the manifest of a project WithNoManifest made.
	errNoManifest = fmt.Errorf("no app.manifest.toml in the project: %w", sdk.ErrNotFound)
	// errComponent reports a component the fake does not declare.
	errComponent = errors.New("cannot declare the component")
	// errNoField reports a field of a component the fake does not declare.
	errNoField = errors.New("the fake declares no such field of a component")
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
	lock      *sdk.LockFile
	build     func(opts sdk.BuildOptions) (sdk.BuildResult, error)
	// buildContext answers Project.BuildContext.
	buildContext func(opts sdk.BuildContextOptions) (*build.Context, build.Build, error)
	// clean answers Project.Clean.
	clean func(opts sdk.CleanOptions) (sdk.CleanResult, error)
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
// default. Project.Manifest returns a copy of it with what AddDependency,
// RemoveDependency, AddComponent, UpdateComponent and RemoveComponent
// changed since, and AddDependency of a name it declares reports Existed
// and its version and registry as Previous and PreviousRegistry.
func WithManifest(manifest sdk.Manifest) Option {
	return func(o *options) { o.manifest = &manifest }
}

// WithNoManifest makes a project without app.manifest.toml: Manifest,
// ReadLock and the edits of components fail with an error wrapping
// sdk.ErrNotFound, and nothing is declared. It cannot be combined with
// WithManifest or WithDeclared.
func WithNoManifest() Option {
	return func(o *options) { o.noMan = true }
}

// WithResolveError makes every Project.Resolve fail with err, as a
// resolution of the lock that fails does; the call is recorded all the
// same.
func WithResolveError(err error) Option {
	return func(o *options) { o.resolve = err }
}

// WithLock makes Project.ReadLock return lock; without it the project has
// no lock, and ReadLock fails with an error wrapping sdk.ErrNotFound.
func WithLock(lock sdk.LockFile) Option {
	return func(o *options) { o.lock = &lock }
}

// WithBuild makes Project.Build call build with the options it was given
// and return what build returns: a module's test writes there what the
// build of its component would make, or fails it. Without it Build builds
// nothing and returns the product and no component; it records the call
// either way (Services.Builds).
func WithBuild(build func(opts sdk.BuildOptions) (sdk.BuildResult, error)) Option {
	return func(o *options) { o.build = build }
}

// WithBuildContext makes Project.BuildContext call buildContext with the
// options it was given and return what it returns, such as the Context of a
// buildtest.Harness and the build of the module's backend. Without it
// BuildContext fails with an error wrapping sdk.ErrNotFound.
func WithBuildContext(
	buildContext func(opts sdk.BuildContextOptions) (*build.Context, build.Build, error),
) Option {
	return func(o *options) { o.buildContext = buildContext }
}

// WithClean makes Project.Clean call clean with the options it was given and
// return what clean returns: a module's test removes there what the clean of
// its component would, or fails it. Without it Clean removes nothing and
// returns an empty result; it records the call either way
// (Services.Cleans).
func WithClean(clean func(opts sdk.CleanOptions) (sdk.CleanResult, error)) Option {
	return func(o *options) { o.clean = clean }
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
	// removed are the names Project.RemoveDependency was given, in order.
	removed []string
	// components are the calls of Project.AddComponent, in order.
	components []sdk.Component
	// updates are the calls of Project.UpdateComponent, in order.
	updates []sdk.ComponentUpdate
	// dropped are the names Project.RemoveComponent was given, in order.
	dropped []string
	// resolves are the calls of Project.Resolve, in order.
	resolves []sdk.ResolveOptions
	// builds are the calls of Project.Build, in order.
	builds []sdk.BuildOptions
	// cleans are the calls of Project.Clean, in order.
	cleans []sdk.CleanOptions
	// locks counts the calls of Project.Lock that took the lock, and held
	// those not released yet.
	locks, held int
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
		lock:      nil,
		build:     nil,
		clean:     nil,

		buildContext: nil,
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
		removed:      nil,
		components:   nil,
		updates:      nil,
		dropped:      nil,
		resolves:     nil,
		builds:       nil,
		cleans:       nil,
		installs:     nil,
		environments: 0,
		indexed:      nil,
		locks:        0,
		held:         0,
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
	if manifest == nil {
		return sdk.Manifest{
			Package:         "",
			Dependencies:    map[string]sdk.ManifestDependency{},
			DevDependencies: map[string]sdk.ManifestDependency{},
			Components:      map[string]sdk.ManifestComponent{},
		}
	}

	out := manifest.Clone()

	if out.Dependencies == nil {
		out.Dependencies = map[string]sdk.ManifestDependency{}
	}

	if out.DevDependencies == nil {
		out.DevDependencies = map[string]sdk.ManifestDependency{}
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
// file: its manifest is what WithManifest and WithDeclared give, which the
// edits of dependencies and components change; they, Resolve, Build,
// Install and PrepareEnvironment record the calls, which the methods of
// Services named after them return, and do what WithMoves,
// WithAddDependencyError, WithNoManifest, WithResolveError, WithLock,
// WithBuild, WithBuildContext and WithInstall say. ScopeDir is .tarantool
// of the root.
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

// RemovedDependencies returns the names Project.RemoveDependency was given
// so far, in order, the failed calls included.
func (s *Services) RemovedDependencies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.removed)
}

// UpdatedComponents returns the updates Project.UpdateComponent was given so
// far, in order, the failed calls included.
func (s *Services) UpdatedComponents() []sdk.ComponentUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.updates)
}

// RemovedComponents returns the names Project.RemoveComponent was given so
// far, in order, the failed calls included.
func (s *Services) RemovedComponents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.dropped)
}

// Resolves returns the options Project.Resolve was given so far, in order,
// the failed calls included.
func (s *Services) Resolves() []sdk.ResolveOptions {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.resolves)
}

// Builds returns the options Project.Build was given so far, in order, the
// failed calls included.
func (s *Services) Builds() []sdk.BuildOptions {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.builds)
}

// Cleans returns the options Project.Clean was given so far, in order, the
// failed calls included.
func (s *Services) Cleans() []sdk.CleanOptions {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.cleans)
}

// Locks returns how many times Project.Lock took the lock of the project so
// far - a call given the context of a lock already held takes none - and
// how many of them are not released yet, in that order: a test of a command
// checks that it took the lock around its work and released it.
func (s *Services) Locks() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.locks, s.held
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
// table declares already takes dep's constraint, and its registry when dep
// has one.
func (s *Services) addDependency(dep sdk.Dependency) (sdk.DependencyChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dep.Registries = slices.Clone(dep.Registries)
	s.added = append(s.added, dep)

	if s.opts.addErr != nil {
		return sdk.DependencyChange{}, s.opts.addErr
	}

	change := sdk.DependencyChange{
		Existed:          false,
		Previous:         "",
		PreviousRegistry: "",
		Moves:            slices.Clone(s.opts.moves),
	}

	table := tableOf(s.manifest, dep.Dev)

	existing, ok := table[dep.Name]
	if !ok {
		table[dep.Name] = declare(dep)

		return change, nil
	}

	change.Existed = true
	change.Previous = existing.Version
	change.PreviousRegistry = existing.Registry

	existing.Version = declare(dep).Version
	if dep.Registry != "" {
		existing.Registry = dep.Registry
	}

	table[dep.Name] = existing

	return change, nil
}

// removeDependency records name and removes it from both tables of the
// manifest as the core would: a name neither declares is an error wrapping
// sdk.ErrNotFound.
func (s *Services) removeDependency(name string) (sdk.DependencyChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removed = append(s.removed, name)

	if s.opts.addErr != nil {
		return sdk.DependencyChange{}, s.opts.addErr
	}

	change := sdk.DependencyChange{
		Existed: false, Previous: "", PreviousRegistry: "", Moves: slices.Clone(s.opts.moves),
	}

	for _, table := range []map[string]sdk.ManifestDependency{
		s.manifest.DevDependencies, s.manifest.Dependencies,
	} {
		existing, ok := table[name]
		if !ok {
			continue
		}

		change.Existed = true
		change.Previous = existing.Version
		change.PreviousRegistry = existing.Registry

		delete(table, name)
	}

	if !change.Existed {
		return sdk.DependencyChange{}, fmt.Errorf("%q: dependency is not declared in the "+
			"manifest: %w", name, sdk.ErrNotFound)
	}

	return change, nil
}

// addComponent records component and declares it as the core would: a
// component of no name, path or backend, a name declared already and a
// project WithNoManifest made are refused.
func (s *Services) addComponent(component sdk.Component) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	component = cloneComponent(component)
	s.components = append(s.components, component)

	switch _, declared := s.manifest.Components[component.Name]; {
	case s.opts.noMan:
		return errNoManifest
	case component.Name == "" || component.Path == "":
		return fmt.Errorf("%w %q: it needs a name and a path", errComponent, component.Name)
	case component.Build != nil && component.Build.Backend == "":
		return fmt.Errorf("%w %q: its build block names no backend", errComponent,
			component.Name)
	case declared:
		return fmt.Errorf("%w %q: the manifest declares it already", errComponent,
			component.Name)
	}

	declared := sdk.ManifestComponent{
		Path: component.Path, Include: slices.Clone(component.Include), Exclude: nil,
		Namespace: s.manifest.Package, Build: nil,
	}

	if component.Build != nil {
		declared.Build = &sdk.ManifestBuild{
			Backend: component.Build.Backend, Cwd: "", Env: nil, Output: nil,
			Incremental: false, Static: false, Fields: nil,
		}

		setBuildFields(declared.Build, component.Build.Fields)
	}

	s.manifest.Components[component.Name] = declared

	return nil
}

// updateComponent records update and applies it to the manifest as the core
// would: the removals, then the fields of the component and of its build
// block, a build block made for a component of none.
func (s *Services) updateComponent(update sdk.ComponentUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	update.Fields = slices.Clone(update.Fields)
	update.Build = slices.Clone(update.Build)
	update.Remove = slices.Clone(update.Remove)
	s.updates = append(s.updates, update)

	if s.opts.noMan {
		return errNoManifest
	}

	component, ok := s.manifest.Components[update.Name]
	if !ok {
		return fmt.Errorf("%w %q: %w", errComponent, update.Name, sdk.ErrNotFound)
	}

	for _, key := range update.Remove {
		removeComponentField(&component, key)
	}

	for _, field := range update.Fields {
		err := setComponentField(&component, field)
		if err != nil {
			return fmt.Errorf("%w %q: %w", errComponent, update.Name, err)
		}
	}

	if len(update.Build) > 0 && component.Build == nil {
		component.Build = &sdk.ManifestBuild{
			Backend: "", Cwd: "", Env: nil, Output: nil, Incremental: false, Static: false,
			Fields: nil,
		}
	}

	if component.Build != nil {
		setBuildFields(component.Build, update.Build)

		if component.Build.Backend == "" {
			return fmt.Errorf("%w %q: its build block names no backend", errComponent,
				update.Name)
		}
	}

	s.manifest.Components[update.Name] = component

	return nil
}

// removeComponent records name and removes the component from the manifest
// as the core would.
func (s *Services) removeComponent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dropped = append(s.dropped, name)

	if s.opts.noMan {
		return errNoManifest
	}

	if _, ok := s.manifest.Components[name]; !ok {
		return fmt.Errorf("%w %q: %w", errComponent, name, sdk.ErrNotFound)
	}

	delete(s.manifest.Components, name)

	return nil
}

// resolve records opts and answers as WithResolveError and WithMoves say.
func (s *Services) resolve(opts sdk.ResolveOptions) (sdk.ResolveChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	opts.Registries = slices.Clone(opts.Registries)
	s.resolves = append(s.resolves, opts)

	switch {
	case s.opts.noMan:
		return sdk.ResolveChange{}, errNoManifest
	case s.opts.resolve != nil:
		return sdk.ResolveChange{}, s.opts.resolve
	}

	return sdk.ResolveChange{Moves: slices.Clone(s.opts.moves)}, nil
}

// readLock returns the lock WithLock gave.
func (s *Services) readLock() (sdk.LockFile, error) {
	if s.opts.noMan {
		return sdk.LockFile{}, errNoManifest
	}

	if s.opts.lock == nil {
		return sdk.LockFile{}, fmt.Errorf("no app.manifest.lock in the project: %w",
			sdk.ErrNotFound)
	}

	lock := *s.opts.lock

	lock.Products = maps.Clone(lock.Products)
	lock.DevDependencies = maps.Clone(lock.DevDependencies)

	return lock, nil
}

// runBuild records opts and runs what WithBuild gave.
func (s *Services) runBuild(opts sdk.BuildOptions) (sdk.BuildResult, error) {
	s.mu.Lock()

	opts.Components = slices.Clone(opts.Components)
	opts.Registries = slices.Clone(opts.Registries)
	opts.Args = slices.Clone(opts.Args)
	s.builds = append(s.builds, opts)

	noMan := s.opts.noMan

	s.mu.Unlock()

	switch {
	case noMan:
		return sdk.BuildResult{}, errNoManifest
	case s.opts.build != nil:
		return s.opts.build(opts)
	}

	product := opts.Product
	if product == "" {
		product = "default"
	}

	return sdk.BuildResult{Product: product, Version: "", Components: nil}, nil
}

// runClean records opts and runs what WithClean gave.
func (s *Services) runClean(opts sdk.CleanOptions) (sdk.CleanResult, error) {
	s.mu.Lock()

	opts.Components = slices.Clone(opts.Components)
	s.cleans = append(s.cleans, opts)

	noMan := s.opts.noMan

	s.mu.Unlock()

	switch {
	case noMan:
		return sdk.CleanResult{}, errNoManifest
	case s.opts.clean != nil:
		return s.opts.clean(opts)
	}

	return sdk.CleanResult{Removed: nil, Cleaned: nil, Hook: false}, nil
}

// lockKey is the key of the context value that carries the lock of the
// fake project.
type lockKey struct{}

// lock takes the lock of the project, unless ctx carries it, and counts it.
func (s *Services) lock(ctx context.Context) (context.Context, func()) {
	if ctx.Value(lockKey{}) != nil {
		return ctx, func() {}
	}

	s.countLock(1)

	var once sync.Once

	return context.WithValue(ctx, lockKey{}, true), func() {
		once.Do(func() { s.countLock(-1) })
	}
}

// countLock counts a lock taken, change 1, or released, change -1.
func (s *Services) countLock(change int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if change > 0 {
		s.locks++
	}

	s.held += change
}

// setBuildFields sets the fields of a build block, as the core reads them
// back: the common ones into their fields, those of the backend into
// Fields, as the TOML decoder reads them.
func setBuildFields(block *sdk.ManifestBuild, fields []sdk.BuildField) {
	for _, field := range fields {
		value := tomlValue(field.Value)

		switch field.Key {
		case "backend":
			block.Backend, _ = value.(string)
		case "cwd":
			block.Cwd, _ = value.(string)
		case "env":
			block.Env = map[string]string{}

			table, _ := value.(map[string]any)
			for key, item := range table {
				block.Env[key], _ = item.(string)
			}
		case "output":
			block.Output = outputOf(value)
		case "incremental":
			block.Incremental, _ = value.(bool)
		case "static":
			block.Static, _ = value.(bool)
		default:
			if block.Fields == nil {
				block.Fields = map[string]any{}
			}

			block.Fields[field.Key] = value
		}
	}
}

// outputOf is the output of a build block whose value, as the TOML decoder
// reads it, is value: an entry alone or an array of them.
func outputOf(value any) []sdk.ManifestOutput {
	entries, ok := value.([]any)
	if !ok {
		entries = []any{value}
	}

	output := make([]sdk.ManifestOutput, 0, len(entries))

	for _, entry := range entries {
		switch entry := entry.(type) {
		case string:
			output = append(output, sdk.ManifestOutput{Path: entry, To: ""})
		case map[string]any:
			path, _ := entry["path"].(string)
			to, _ := entry["to"].(string)

			output = append(output, sdk.ManifestOutput{Path: path, To: to})
		}
	}

	return output
}

// setComponentField sets a field of [components.<name>] of component.
func setComponentField(component *sdk.ManifestComponent, field sdk.BuildField) error {
	value := tomlValue(field.Value)

	switch field.Key {
	case "path":
		component.Path, _ = value.(string)
	case "include":
		component.Include = stringsOf(value)
	case "exclude":
		component.Exclude = stringsOf(value)
	case "namespace":
		component.Namespace, _ = value.(string)
	default:
		return fmt.Errorf("%w: %q", errNoField, field.Key)
	}

	return nil
}

// removeComponentField removes the field key of ComponentUpdate.Remove
// from component.
func removeComponentField(component *sdk.ManifestComponent, key string) {
	switch key {
	case "include":
		component.Include = nil
	case "exclude":
		component.Exclude = nil
	case "build":
		component.Build = nil
	}

	field, ok := strings.CutPrefix(key, "build.")
	if !ok || component.Build == nil {
		return
	}

	switch field {
	case "cwd":
		component.Build.Cwd = ""
	case "env":
		component.Build.Env = nil
	case "output":
		component.Build.Output = nil
	case "incremental":
		component.Build.Incremental = false
	case "static":
		component.Build.Static = false
	default:
		delete(component.Build.Fields, field)
	}
}

// stringsOf is value, an array as the TOML decoder reads it, as strings.
func stringsOf(value any) []string {
	items, _ := value.([]any)

	out := make([]string, 0, len(items))
	for _, item := range items {
		str, _ := item.(string)

		out = append(out, str)
	}

	return out
}

// tomlValue is value, of a BuildField, as the TOML decoder reads what the
// core writes of it: an integer is an int64, a slice a []any and a map with
// string keys a map[string]any, of such values.
func tomlValue(value any) any {
	return tomlOf(reflect.ValueOf(value))
}

// tomlOf is tomlValue of a reflected value.
func tomlOf(value reflect.Value) any {
	if value.Kind() == reflect.Interface && !value.IsNil() {
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.String:
		return value.String()
	case reflect.Bool:
		return value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(value.Uint()) //nolint:gosec // A field of a manifest is no larger.
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, value.Len())
		for i := range value.Len() {
			out = append(out, tomlOf(value.Index(i)))
		}

		return out
	case reflect.Map:
		out := make(map[string]any, value.Len())
		for _, key := range value.MapKeys() {
			out[key.String()] = tomlOf(value.MapIndex(key))
		}

		return out
	default:
		if !value.IsValid() {
			return nil
		}

		return value.Interface()
	}
}

// cloneComponent returns a copy of component that shares no slice with it.
func cloneComponent(component sdk.Component) sdk.Component {
	component.Include = slices.Clone(component.Include)

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
func (f fakeProject) AddComponent(_ context.Context, component sdk.Component) error {
	return f.services.addComponent(component)
}

// UpdateComponent records update and applies it to the manifest of the
// project.
func (f fakeProject) UpdateComponent(_ context.Context, update sdk.ComponentUpdate) error {
	return f.services.updateComponent(update)
}

// RemoveComponent records name and removes the component from the manifest
// of the project.
func (f fakeProject) RemoveComponent(_ context.Context, name string) error {
	return f.services.removeComponent(name)
}

// Resolve records opts and answers as WithResolveError and WithMoves say.
func (f fakeProject) Resolve(_ context.Context, opts sdk.ResolveOptions) (
	sdk.ResolveChange, error,
) {
	return f.services.resolve(opts)
}

// ReadLock returns the lock WithLock gave.
func (f fakeProject) ReadLock() (sdk.LockFile, error) { return f.services.readLock() }

// Build records opts and runs what WithBuild gave.
func (f fakeProject) Build(_ context.Context, opts sdk.BuildOptions) (sdk.BuildResult, error) {
	return f.services.runBuild(opts)
}

// BuildContext runs what WithBuildContext gave.
func (f fakeProject) BuildContext(
	_ context.Context, opts sdk.BuildContextOptions,
) (*build.Context, build.Build, error) {
	if f.services.opts.buildContext == nil {
		return nil, nil, fmt.Errorf("the fake has no Context of component %q (WithBuildContext): "+
			"%w", opts.Component, sdk.ErrNotFound)
	}

	return f.services.opts.buildContext(opts)
}

// Clean records opts and runs what WithClean gave.
func (f fakeProject) Clean(_ context.Context, opts sdk.CleanOptions) (sdk.CleanResult, error) {
	return f.services.runClean(opts)
}

// Lock counts the lock and returns the context that carries it; nothing
// waits, there being nothing else to wait for (Services.Locks).
func (f fakeProject) Lock(ctx context.Context) (context.Context, func(), error) {
	locked, release := f.services.lock(ctx)

	return locked, release, nil
}

// RemoveDependency records name and removes it from the manifest of the
// project.
func (f fakeProject) RemoveDependency(
	_ context.Context, name string, _ sdk.ResolveOptions,
) (sdk.DependencyChange, error) {
	return f.services.removeDependency(name)
}

// ScopeDir returns .tarantool of the root.
func (f fakeProject) ScopeDir() (string, error) {
	return scope.ProjectRoot(f.services.opts.root), nil
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
