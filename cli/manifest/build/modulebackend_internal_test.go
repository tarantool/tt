package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// This file builds components and hooks with backends a module provides,
// registered with the registry of the build as a module registers them,
// through the whole pipeline: the preflight, the Context of each build, the
// layout with the exclusions of the backend, the stage and its record of
// what each build wrote into its output and share directories, the
// incremental skip, the programs of an executable component and the hooks.

// touchConfig is the configuration of the touch backend. A block names any
// of the files, and the build makes those it names.
type touchConfig struct {
	// Module is the module the build makes, into the output directory.
	Module string `toml:"module"`
	// Data is a data file the build makes, into the share directory.
	Data string `toml:"data"`
	// Program is the program the build makes, into the directory of the
	// programs.
	Program string `toml:"program"`
	// StrayModule and StrayProgram are a module and a program the build
	// makes without saying it does (sdkbuild.Producer, ProgramNamer).
	StrayModule  string `toml:"stray_module"`
	StrayProgram string `toml:"stray_program"`
}

// touchRecorder records the Contexts the builds of touch ran in.
type touchRecorder struct {
	mu       sync.Mutex
	contexts map[string]sdkbuild.Context
}

// ran returns the facts of the last build of the component name.
func (r *touchRecorder) ran(name string) (sdkbuild.Context, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	facts, ok := r.contexts[name]

	return facts, ok
}

// record records the facts of c, under name.
func (r *touchRecorder) record(name string, c *sdkbuild.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.contexts[name] = *c
}

// touchBuild is a checked block of touch.
type touchBuild struct {
	config   touchConfig
	recorder *touchRecorder
}

// Preflight finds sh in the PATH of the build.
func (b touchBuild) Preflight(c *sdkbuild.Context) error {
	_, err := c.LookPath("sh")
	if err != nil {
		return fmt.Errorf("%w: sh", sdkbuild.ErrNoTool)
	}

	return nil
}

// Produces names the types of the files the block names, but the stray
// ones.
func (b touchBuild) Produces() []sdkbuild.Result {
	var results []sdkbuild.Result

	for result, file := range map[sdkbuild.Result]string{
		sdkbuild.Module: b.config.Module, sdkbuild.Data: b.config.Data,
		sdkbuild.Program: b.config.Program,
	} {
		if file != "" {
			results = append(results, result)
		}
	}

	return results
}

// Programs names the program of the block, but not the stray one: the list
// is not exhaustive.
func (b touchBuild) Programs() ([]string, bool) {
	if b.config.Program == "" {
		return nil, false
	}

	return []string{b.config.Program}, false
}

// Inputs adds the version of the backend to what an incremental build is
// skipped by.
func (b touchBuild) Inputs(*sdkbuild.Context) ([]string, error) {
	return []string{"touch 1"}, nil
}

// Run makes the files of the block: a module whose content is what a command
// of the build printed, its TT_COMPONENT_NAME, a data file and programs,
// copies of sh. It counts its runs in the state directory of the build.
func (b touchBuild) Run(ctx context.Context, facts *sdkbuild.Context) error {
	b.recorder.record(facts.Component.Name, facts)

	runs := filepath.Join(facts.Dirs.State, facts.Component.Name+".runs")
	data, _ := os.ReadFile(runs)

	err := os.WriteFile(runs, append(data, '.'), 0o600)
	if err != nil {
		return err
	}

	shell, err := exec.LookPath("sh")
	if err == nil {
		shell, err = filepath.EvalSymlinks(shell)
	}

	if err != nil {
		return err
	}

	for _, program := range []string{b.config.Program, b.config.StrayProgram} {
		if program == "" {
			continue
		}

		err = sdkbuild.CopyFile(filepath.Join(facts.Dirs.Programs, program), shell)
		if err != nil {
			return err
		}
	}

	out, err := facts.Output(facts.Command(ctx, "sh", "-c", `printf %s "$TT_COMPONENT_NAME"`))
	if err != nil {
		return err
	}

	for _, module := range []string{b.config.Module, b.config.StrayModule} {
		if module == "" {
			continue
		}

		err = os.WriteFile(filepath.Join(facts.Dirs.Output, module), out, 0o600)
		if err != nil {
			return err
		}
	}

	if b.config.Data != "" {
		err = os.WriteFile(filepath.Join(facts.Dirs.Share, b.config.Data), []byte("data"),
			0o600)
	}

	return err
}

// touchBackend is the touch backend, which makes modules, data files and
// programs incrementally, its tool leaving out/ in the component, with
// builds that record their Contexts into recorder.
func touchBackend(recorder *touchRecorder) sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:     "touch",
		Produces: sdkbuild.Results(),
		Config:   func() any { return new(touchConfig) },
		New: func(cfg any, _ sdkbuild.Component, _ sdkbuild.Common) (sdkbuild.Build, error) {
			config, ok := cfg.(*touchConfig)
			if !ok {
				return nil, sdkbuild.FieldErrorf("", "not a configuration of touch")
			}

			if config.Data != "" && !strings.HasSuffix(config.Data, ".wasm") {
				return nil, sdkbuild.FieldErrorf("data", "%q is not a .wasm file", config.Data)
			}

			return touchBuild{config: *config, recorder: recorder}, nil
		},
		Incremental:    true,
		OutputFrom:     sdkbuild.OutputNone,
		Exclude:        []string{"/out/"},
		NoSourceLayout: false,
		Hook:           false,
	}
}

// touchManifest is a project with a component of the touch backend, which a
// module provides, that makes a module, a data file and a program, and a
// component of the deprecated kind = "executable" that makes a program.
const touchManifest = manifestHead + `
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'touch'
module = 'fast.so'
data = 'fast.wasm'
program = 'fast-tool'
incremental = true

[components.tool]
path = 'tool'
kind = 'executable'

[components.tool.build]
backend = 'touch'
program = 'touch-tool'
`

// runs returns how many times the build of the component name ran in the
// project at dir.
func runs(t *testing.T, dir, name string) int {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, buildDirName, name+".runs"))
	if os.IsNotExist(err) {
		return 0
	}

	require.NoError(t, err)

	return len(data)
}

// TestRun_moduleBackend checks that a backend a module provides builds a
// module and an executable component through the whole pipeline: in the
// Context the core makes for it, with what it wrote into the output and the
// share directories laid out in the tree and kept by the stage, the files
// its tool leaves in the component left out, its incremental component
// skipped while its inputs stay, and the program it names recorded.
func TestRun_moduleBackend(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, touchManifest, map[string]string{
		"fast/init.lua":     "return {}\n",
		"fast/out/stale.so": "what the tool left",
		"tool/main.txt":     "the sources of the tool\n",
	})

	recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("touch", []sdkbuild.Backend{touchBackend(recorder)}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends

	result, err := RunResult(context.Background(), opts)
	require.NoError(t, err)

	tree := filepath.Join(dir, rocksDirName)
	stage := packageStage(dir, "default")

	module, err := os.ReadFile(filepath.Join(tree, "lib", "tarantool", "my-app", "fast.so"))
	require.NoError(t, err)
	assert.Equal(t, "fast", string(module), "made by a command of the build")
	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "fast.wasm"))
	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "init.lua"))
	assert.NoFileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "out", "stale.so"),
		"what the tool leaves in the component is not laid out")
	assert.FileExists(t, filepath.Join(tree, "bin", "touch-tool"))
	assert.FileExists(t, filepath.Join(tree, "bin", "fast-tool"))
	assert.Equal(t, map[string][]string{"fast": {"fast-tool"}, "tool": {"touch-tool"}},
		result.Programs)

	// The record of the build has what each component made, by type.
	assert.Equal(t, map[string]madeFiles{
		"fast": {
			Modules: []string{"fast.so"}, Data: []string{"fast.wasm"},
			Programs: []string{"fast-tool"},
		},
		"tool": {Programs: []string{"touch-tool"}},
	}, recordOf(t, dir))
	assert.Equal(t, map[string][]string{"fast": {"fast-tool"}, "tool": {"touch-tool"}},
		RecordedPrograms(dir))

	fast, ok := recorder.ran("fast")
	require.True(t, ok)
	assert.Equal(t, sdkbuild.Component{
		Name: "fast", Path: "fast", Namespace: "my-app", Package: "my-app", Expects: nil,
	}, fast.Component)
	assert.Equal(t, sdkbuild.Dirs{
		Project:   dir,
		Component: filepath.Join(dir, "fast"),
		Cwd:       filepath.Join(dir, "fast"),
		Work:      filepath.Join(dir, buildDirName, componentDirName, "fast"),
		Output:    filepath.Join(stage, "lib", "my-app"),
		Share:     filepath.Join(stage, "lua", "my-app"),
		Programs:  filepath.Join(stage, "bin"),
		State:     filepath.Join(dir, buildDirName),
		Tools:     ProjectBinDir(dir),
		Tree:      tree,
	}, fast.Dirs)
	assert.False(t, fast.Hook)
	assert.Equal(t, "/nonexistent", fast.Tarantool.Prefix)
	assert.Equal(t, "fast", fast.Env["TT_COMPONENT_NAME"])
	assert.Equal(t, "1.2.3", fast.Env["TT_VERSION"])
	assert.Equal(t, fast.Dirs.Output, fast.Env["TT_OUTPUT_DIR"])

	// A component of kind = "executable" has the directories of any other,
	// and is told to put its programs into TT_OUTPUT_DIR.
	tool, ok := recorder.ran("tool")
	require.True(t, ok)
	assert.Equal(t, []sdkbuild.Result{sdkbuild.Program}, tool.Component.Expects)
	assert.Equal(t, filepath.Join(stage, "bin"), tool.Dirs.Programs)
	assert.Equal(t, filepath.Join(stage, "lib", "my-app"), tool.Dirs.Output)
	assert.Equal(t, tool.Dirs.Programs, tool.Env["TT_OUTPUT_DIR"])

	assert.Equal(t, 1, runs(t, dir, "fast"))
	assert.Equal(t, 1, runs(t, dir, "tool"))

	// The incremental component is skipped, and what it made of each type
	// stays in the stage and in the tree; the other is built again.
	result, err = RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, runs(t, dir, "fast"))
	assert.Equal(t, 2, runs(t, dir, "tool"))

	stayed := func() {
		t.Helper()

		for _, file := range []string{
			filepath.Join(stage, "lib", "my-app", "fast.so"),
			filepath.Join(stage, "lua", "my-app", "fast.wasm"),
			filepath.Join(stage, "bin", "fast-tool"),
			filepath.Join(tree, "lib", "tarantool", "my-app", "fast.so"),
			filepath.Join(tree, "share", "tarantool", "my-app", "fast.wasm"),
			filepath.Join(tree, "bin", "fast-tool"),
			filepath.Join(tree, "bin", "touch-tool"),
		} {
			assert.FileExists(t, file)
		}
	}

	stayed()
	assert.Equal(t, map[string][]string{"fast": {"fast-tool"}, "tool": {"touch-tool"}},
		result.Programs)

	// A build of the other component alone keeps what fast made.
	only := opts

	only.Component = "tool"

	result, err = RunResult(context.Background(), only)
	require.NoError(t, err)
	assert.Equal(t, 1, runs(t, dir, "fast"))
	stayed()
	assert.Equal(t, map[string][]string{"fast": {"fast-tool"}, "tool": {"touch-tool"}},
		result.Programs)

	// A skipped build whose program went from the stage builds again.
	require.NoError(t, os.Remove(filepath.Join(stage, "bin", "fast-tool")))

	_, err = RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 2, runs(t, dir, "fast"))
	stayed()

	// A changed source builds it again.
	writeFile(t, filepath.Join(dir, "fast", "init.lua"), "return { changed = true }\n")

	_, err = RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 3, runs(t, dir, "fast"))
	stayed()

	// What the build no longer makes goes from the stage and the tree with
	// a build from scratch. Before it, a file an earlier build made counts
	// as made while it is there, as one of a make target that is up to date
	// does.
	writeFile(t, filepath.Join(dir, manifestFileName),
		strings.Replace(touchManifest, "program = 'fast-tool'\n", "", 1))

	rebuild := opts

	rebuild.Rebuild = true

	result, err = RunResult(context.Background(), rebuild)
	require.NoError(t, err)
	assert.Equal(t, 4, runs(t, dir, "fast"))
	assert.NoFileExists(t, filepath.Join(stage, "bin", "fast-tool"))
	assert.NoFileExists(t, filepath.Join(tree, "bin", "fast-tool"))
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "fast.so"))
	assert.Equal(t, map[string][]string{"tool": {"touch-tool"}}, result.Programs)
}

// TestRun_kindChecksTheBuild checks the deprecated kind of a component as
// what it says of its build: a warning, and an error of exit code 2 when the
// build makes what the kind rules out but did not say it would.
func TestRun_kindChecksTheBuild(t *testing.T) {
	t.Parallel()

	recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("touch", []sdkbuild.Backend{touchBackend(recorder)}, nil))

	for _, test := range []struct {
		name, kind, block string
		err               error
		msg               string
	}{
		{
			"a module of kind = executable", "executable",
			"program = 'gw'\nstray_module = 'gw.so'\n", errNotOnlyPrograms,
			`building component "gw": kind = "executable", but the build made more than ` +
				`programs: lib/my-app/gw.so`,
		},
		{
			"a program of kind = module", "module",
			"module = 'gw.so'\nstray_program = 'gw'\n", errProgramOfModule,
			`building component "gw": kind = "module", but the build made programs: bin/gw`,
		},
		{
			"nothing of kind = executable", "executable", "", errNoProgram,
			`building component "gw": no program built`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := setupProject(t, manifestHead+"\n[components.gw]\npath = 'gw'\nkind = '"+
				test.kind+"'\n\n[components.gw.build]\nbackend = 'touch'\n"+test.block,
				map[string]string{"gw/.keep": ""})

			var warnings []string

			opts := dryOptions(dir)

			opts.Backends = backends
			opts.Warn = func(warning string) { warnings = append(warnings, warning) }

			err := Run(context.Background(), opts)
			require.ErrorIs(t, err, test.err)
			assert.Contains(t, err.Error(), test.msg)
			assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
			assert.Contains(t, warnings, "components.gw.kind is deprecated: a component "+
				"makes what its build makes; remove it")
		})
	}

	// What the build says it makes is checked before anything is built.
	dir := setupProject(t, manifestHead+"\n[components.gw]\npath = 'gw'\nkind = 'executable'"+
		"\n\n[components.gw.build]\nbackend = 'touch'\nprogram = 'gw'\nmodule = 'gw.so'\n",
		map[string]string{"gw/.keep": ""})

	opts := dryOptions(dir)

	opts.Backends = backends

	err := Run(context.Background(), opts)
	require.ErrorContains(t, err,
		`components.gw.kind: is "executable", but the build makes modules`)
	assert.Equal(t, 0, runs(t, dir, "gw"))
}

// TestRun_moduleBackendFieldErrors checks that the fields of a block of a
// backend a module provides are checked as those of the core: one of
// another backend names it, one no backend has is unknown, and New's error
// names the field.
func TestRun_moduleBackendFieldErrors(t *testing.T) {
	t.Parallel()

	recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("touch", []sdkbuild.Backend{touchBackend(recorder)}, nil))

	for _, test := range []struct {
		name, block, msg string
	}{
		{
			"a field of another backend", "module = 'x.so'\nmake_target = 'all'\n",
			`components.fast.build.make_target: is a field of backend make, not of "touch"`,
		},
		{
			"an unknown field", "module = 'x.so'\njobs = 4\n",
			`components.fast.build.jobs: unknown field in manifest version "0.1"`,
		},
		{
			"a field of a wrong type", "module = 1\n",
			"components.fast.build.module: must be a string, not an integer",
		},
		{
			"an error of New", "data = 'x.txt'\n",
			`components.fast.build.data: "x.txt" is not a .wasm file`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := setupProject(t, manifestHead+"\n[components.fast]\npath = 'fast'\n\n"+
				"[components.fast.build]\nbackend = 'touch'\n"+test.block, nil)

			opts := dryOptions(dir)

			opts.Backends = backends

			err := Run(context.Background(), opts)
			require.EqualError(t, err, "invalid manifest: "+test.msg)
		})
	}
}

// hookRecorder is a backend that runs hooks, recording their Contexts and
// what a command of theirs is told.
type hookRecorder struct {
	mu       sync.Mutex
	contexts []sdkbuild.Context
	printed  []string
}

// hookBuild is a checked hook block of the hook backend.
type hookBuild struct {
	recorder *hookRecorder
}

// Run records the Context of the hook and the variables of the contract a
// command of it gets: unset prints as "-".
func (b hookBuild) Run(ctx context.Context, facts *sdkbuild.Context) error {
	out, err := facts.Output(facts.Command(ctx, "sh", "-c", `printf %s "`+
		`${TT_OUTPUT_DIR--}|${TT_COMPONENT_NAME--}|${TT_BUILD_DIR--}|${TT_PACKAGE--}|`+
		`${TT_VERSION--}|$(pwd -P)"`))
	if err != nil {
		return err
	}

	b.recorder.mu.Lock()
	defer b.recorder.mu.Unlock()

	b.recorder.contexts = append(b.recorder.contexts, *facts)
	b.recorder.printed = append(b.recorder.printed, string(out))

	return nil
}

// hookBackend is the hook backend of a module, which runs hooks only.
func hookBackend(recorder *hookRecorder) sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:     "hooker",
		Produces: []sdkbuild.Result{sdkbuild.Module},
		Config:   func() any { return new(struct{}) },
		New: func(_ any, component sdkbuild.Component, _ sdkbuild.Common) (
			sdkbuild.Build, error,
		) {
			if component.Name != "" {
				return nil, sdkbuild.FieldErrorf("", "hooker runs hooks only")
			}

			return hookBuild{recorder: recorder}, nil
		},
		Incremental: false,
		OutputFrom:  sdkbuild.OutputNone,
		Exclude:     nil,
		Hook:        true,
	}
}

// TestRun_hookOfAModuleBackend checks that the hooks run through the
// registry, a backend a module provides among the others, in hook mode: a
// Context with no component and none of its directories, and the reduced
// contract of a hook.
func TestRun_hookOfAModuleBackend(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[hooks.pre_build]
backend = 'hooker'

[hooks.post_build]
backend = 'hooker'
cwd = 'gen'
`, map[string]string{"gen/.keep": ""})

	recorder := &hookRecorder{mu: sync.Mutex{}, contexts: nil, printed: nil}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("hooks", []sdkbuild.Backend{hookBackend(recorder)}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends

	require.NoError(t, Run(context.Background(), opts))

	physical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"-|-|-|my-app|1.2.3|" + physical,
		"-|-|-|my-app|1.2.3|" + filepath.Join(physical, "gen"),
	}, recorder.printed)

	require.Len(t, recorder.contexts, 2)

	for _, facts := range recorder.contexts {
		assert.True(t, facts.Hook)
		assert.Equal(t, sdkbuild.Component{}, facts.Component) //nolint:exhaustruct // None.
		assert.Empty(t, facts.Dirs.Component)
		assert.Empty(t, facts.Dirs.Work)
		assert.Empty(t, facts.Dirs.Output)
		assert.Empty(t, facts.Dirs.Share)
		assert.Equal(t, dir, facts.Dirs.Project)
		assert.NotContains(t, facts.Env, "TT_OUTPUT_DIR")
		assert.NotContains(t, facts.Env, "TT_COMPONENT_NAME")
		assert.NotContains(t, facts.Env, "TT_BUILD_DIR")
		assert.Equal(t, "my-app", facts.Env["TT_PACKAGE"])
	}

	assert.Equal(t, filepath.Join(dir, "gen"), recorder.contexts[1].Dirs.Cwd)
}

// TestRun_hookOfABackendOfComponents checks that a hook may not name a
// backend that runs no hooks, one of the core's or of a module's.
func TestRun_hookOfABackendOfComponents(t *testing.T) {
	t.Parallel()

	for _, backend := range []string{"cmake", "touch"} {
		dir := setupProject(t, manifestHead+"\n[hooks.pre_build]\nbackend = '"+backend+"'\n",
			nil)

		recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

		backends := buildregtest.Registry()
		require.NoError(t, backends.Add("touch", []sdkbuild.Backend{touchBackend(recorder)},
			nil))

		opts := dryOptions(dir)

		opts.Backends = backends

		err := Run(context.Background(), opts)
		require.EqualError(t, err, `invalid manifest: hooks.pre_build: backend "`+backend+
			`" is not supported (want make or shell)`)
	}
}

// TestRun_shellHookContract checks the reduced contract of a shell hook: no
// output directory, component and directory of intermediate files.
func TestRun_shellHookContract(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[hooks.pre_build]
command = 'sh'
args = ['-c', '''printf %s "${TT_OUTPUT_DIR--}|${TT_COMPONENT_NAME--}|\
${TT_BUILD_DIR--}|${TT_PACKAGE}" > hook.txt''']
`, nil)

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	data, err := os.ReadFile(filepath.Join(dir, "hook.txt"))
	require.NoError(t, err)
	assert.Equal(t, "-|-|-|my-app", string(data))
}

// TestRun_outputDirsExistBeforeTheBuild checks that the core creates the
// output directory of a build before it runs: a command writes into
// TT_OUTPUT_DIR with no mkdir of its own, that of a module component and of
// an executable one alike.
func TestRun_outputDirsExistBeforeTheBuild(t *testing.T) {
	t.Parallel()

	shell, err := exec.LookPath("sh")
	require.NoError(t, err)

	shell, err = filepath.EvalSymlinks(shell)
	require.NoError(t, err)

	dir := setupProject(t, manifestHead+fmt.Sprintf(`
[components.native]
path = 'native'

[components.native.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'echo built > "$TT_OUTPUT_DIR/fast.so"']

[components.tool]
path = 'tool'
kind = 'executable'

[components.tool.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'cp "$1" "$TT_OUTPUT_DIR/tool"', 'sh', %s]
`, strconv.Quote(shell)), map[string]string{"native/.keep": "", "tool/.keep": ""})

	result, err := RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, rocksDirName, "lib", "tarantool", "my-app", "fast.so"))
	assert.Equal(t, map[string][]string{"tool": {"tool"}}, result.Programs)
}

// TestRun_outputOfEveryType checks that a make or shell build puts each file
// of output where its entry says: a string and to = "lib" into lib/ of the
// namespace, to = "share" into share/ of it, to = "bin" into bin/, recorded
// as a program of the component; and that an incremental build skipped
// keeps them all, in the stage and in the tree.
func TestRun_outputOfEveryType(t *testing.T) {
	t.Parallel()

	shell, err := exec.LookPath("sh")
	require.NoError(t, err)

	shell, err = filepath.EvalSymlinks(shell)
	require.NoError(t, err)

	script := `mkdir -p build && cp "` + shell + `" build/tool && echo so > build/tool.so && ` +
		`echo lib > build/extra.so && printf '\0asm\1\0\0\0' > build/tool.wasm && ` +
		`echo . >> ../builds.log`
	output := `output = ['build/tool.so', { path = 'build/extra.so', to = 'lib' }, ` +
		`{ path = 'build/tool.wasm', to = 'share' }, { path = 'build/tool', to = 'bin' }]` + "\n"

	for name, block := range map[string]string{
		"shell": "backend = 'shell'\ncommand = 'sh'\nargs = ['-c', '''" + script + "''']\n",
		"make":  "backend = 'make'\nmake_target = 'all'\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := setupProject(t, manifestHead+"\n[components.tool]\npath = 'tool'\n"+
				"namespace = 'tools'\n\n[components.tool.build]\n"+block+output+
				"incremental = true\n", map[string]string{
				"tool/init.lua": "return {}\n",
				"tool/Makefile": "all:\n\t" + strings.ReplaceAll(script, "$", "$$") + "\n",
				// What the build makes in the sources is no input of it.
				"tool/.gitignore": "/build/\n",
			})

			if name == "make" {
				requireMake(t)
			}

			tree := filepath.Join(dir, rocksDirName)
			stage := packageStage(dir, "default")

			for build := 1; build <= 2; build++ {
				result, err := RunResult(context.Background(), dryOptions(dir))
				require.NoError(t, err, build)

				for _, file := range []string{
					filepath.Join(stage, "lib", "tools", "tool.so"),
					filepath.Join(stage, "lib", "tools", "extra.so"),
					filepath.Join(stage, "lua", "tools", "tool.wasm"),
					filepath.Join(stage, "lua", "tools", "init.lua"),
					filepath.Join(stage, "bin", "tool"),
					filepath.Join(tree, "lib", "tarantool", "tools", "tool.so"),
					filepath.Join(tree, "lib", "tarantool", "tools", "extra.so"),
					filepath.Join(tree, "share", "tarantool", "tools", "tool.wasm"),
					filepath.Join(tree, "bin", "tool"),
				} {
					assert.FileExists(t, file, build)
				}

				assert.NoFileExists(t, filepath.Join(stage, "lib", "tools", "tool"))
				assert.NoFileExists(t, filepath.Join(stage, "bin", "tool.so"))
				assert.Equal(t, map[string][]string{"tool": {"tool"}}, result.Programs)
				assert.Equal(t, madeFiles{
					Modules: []string{"extra.so", "tool.so"}, Data: []string{"tool.wasm"},
					Programs: []string{"tool"},
				}, recordOf(t, dir)["tool"])
			}

			// The second build was skipped.
			data, err := os.ReadFile(filepath.Join(dir, "builds.log"))
			require.NoError(t, err)
			assert.Equal(t, ".\n", string(data))
		})
	}
}

// requireMake skips the test where make is not installed.
func requireMake(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not available")
	}
}

// TestRun_missingDeclaredOutput checks that a file output lists that the
// build of make or shell did not make fails it, with exit code 2.
func TestRun_missingDeclaredOutput(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[components.native]
path = 'native'

[components.native.build]
backend = 'shell'
command = 'true'
output = ['missing.so']
`, map[string]string{"native/.keep": ""})

	err := Run(context.Background(), dryOptions(dir))
	require.ErrorContains(t, err, `building component "native": copy declared output `+
		`"missing.so"`)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
}
