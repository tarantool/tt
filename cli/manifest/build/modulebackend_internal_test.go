package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
)

// This file builds components and hooks with backends a module provides,
// registered with the registry of the build as a module registers them,
// through the whole pipeline: the preflight, the Context of each build, the
// layout with the exclusions of the backend, the stage and its record of
// what each build wrote into its output and share directories, the
// incremental skip, the programs of an executable component and the hooks.

// touchConfig is the configuration of the touch backend.
type touchConfig struct {
	// Module is the module a module component makes, into the output
	// directory.
	Module string `toml:"module"`
	// Data is a data file a module component makes, into the share
	// directory.
	Data string `toml:"data"`
	// Program is the program an executable component makes.
	Program string `toml:"program"`
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

// Programs names the program of an executable component.
func (b touchBuild) Programs() ([]string, bool) {
	return []string{b.config.Program}, true
}

// Inputs adds the version of the backend to what an incremental build is
// skipped by.
func (b touchBuild) Inputs(*sdkbuild.Context) ([]string, error) {
	return []string{"touch 1"}, nil
}

// Run makes the files of the block: a module whose content is what a command
// of the build printed, its TT_COMPONENT_NAME, a data file, or a program, a
// copy of sh. It counts its runs in the state directory of the build.
func (b touchBuild) Run(ctx context.Context, facts *sdkbuild.Context) error {
	b.recorder.record(facts.Component.Name, facts)

	runs := filepath.Join(facts.Dirs.State, facts.Component.Name+".runs")
	data, _ := os.ReadFile(runs)

	err := os.WriteFile(runs, append(data, '.'), 0o600)
	if err != nil {
		return err
	}

	if b.config.Program != "" {
		shell, err := exec.LookPath("sh")
		if err == nil {
			shell, err = filepath.EvalSymlinks(shell)
		}

		if err != nil {
			return err
		}

		return sdkbuild.CopyFile(filepath.Join(facts.Dirs.Output, b.config.Program), shell)
	}

	out, err := facts.Output(facts.Command(ctx, "sh", "-c", `printf %s "$TT_COMPONENT_NAME"`))
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(facts.Dirs.Output, b.config.Module), out, 0o600)
	if err == nil && b.config.Data != "" {
		err = os.WriteFile(filepath.Join(facts.Dirs.Share, b.config.Data), []byte("data"),
			0o600)
	}

	return err
}

// touchBackend is the touch backend, which builds either kind incrementally,
// its tool leaving out/ in the component, with builds that record their
// Contexts into recorder.
func touchBackend(recorder *touchRecorder) sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:   "touch",
		Kinds:  []string{sdkbuild.KindModule, sdkbuild.KindExecutable},
		Config: func() any { return new(touchConfig) },
		New: func(cfg any, component sdkbuild.Component, _ sdkbuild.Common) (
			sdkbuild.Build, error,
		) {
			config, ok := cfg.(*touchConfig)
			if !ok {
				return nil, sdkbuild.FieldErrorf("", "not a configuration of touch")
			}

			if component.Kind == sdkbuild.KindExecutable && config.Program == "" {
				return nil, sdkbuild.FieldErrorf("", "program is required for kind %q",
					component.Kind)
			}

			if component.Kind == sdkbuild.KindModule && config.Module == "" {
				return nil, sdkbuild.FieldErrorf("", "module is required for kind %q",
					component.Kind)
			}

			return touchBuild{config: *config, recorder: recorder}, nil
		},
		Incremental: true,
		OutputFrom:  sdkbuild.OutputNone,
		Exclude:     []string{"/out/"},
		Hook:        false,
	}
}

// touchManifest is a project with a module component and an executable
// component of the touch backend, which a module provides.
const touchManifest = manifestHead + `
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'touch'
module = 'fast.so'
data = 'fast.wasm'
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

	backends := buildreg.Core()
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
	assert.Equal(t, map[string][]string{"tool": {"touch-tool"}}, result.Executables)

	fast, ok := recorder.ran("fast")
	require.True(t, ok)
	assert.Equal(t, sdkbuild.Component{
		Name: "fast", Kind: sdkbuild.KindModule, Path: "fast", Namespace: "my-app",
		Package: "my-app",
	}, fast.Component)
	assert.Equal(t, sdkbuild.Dirs{
		Project:   dir,
		Component: filepath.Join(dir, "fast"),
		Cwd:       filepath.Join(dir, "fast"),
		Work:      filepath.Join(dir, buildDirName, componentDirName, "fast"),
		Output:    filepath.Join(stage, "lib", "my-app"),
		Share:     filepath.Join(stage, "lua", "my-app"),
		State:     filepath.Join(dir, buildDirName),
		Bin:       ProjectBinDir(dir),
		Tree:      tree,
	}, fast.Dirs)
	assert.False(t, fast.Hook)
	assert.Equal(t, "/nonexistent", fast.Tarantool.Prefix)
	assert.Equal(t, "fast", fast.Env["TT_COMPONENT_NAME"])
	assert.Equal(t, "1.2.3", fast.Env["TT_VERSION"])

	tool, ok := recorder.ran("tool")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(stage, "bin"), tool.Dirs.Output)
	assert.Empty(t, tool.Dirs.Share, "an executable component lays out no files")
	assert.Empty(t, tool.Component.Namespace)

	assert.Equal(t, 1, runs(t, dir, "fast"))
	assert.Equal(t, 1, runs(t, dir, "tool"))

	// The incremental component is skipped, and what it made stays in the
	// stage and in the tree; the other is built again.
	result, err = RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, runs(t, dir, "fast"))
	assert.Equal(t, 2, runs(t, dir, "tool"))
	assert.FileExists(t, filepath.Join(stage, "lua", "my-app", "fast.wasm"))
	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "fast.wasm"))
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "fast.so"))
	assert.Equal(t, map[string][]string{"tool": {"touch-tool"}}, result.Executables)

	// A changed source builds it again.
	writeFile(t, filepath.Join(dir, "fast", "init.lua"), "return { changed = true }\n")

	_, err = RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 2, runs(t, dir, "fast"))
}

// TestRun_moduleBackendFieldErrors checks that the fields of a block of a
// backend a module provides are checked as those of the core: one of
// another backend names it, one no backend has is unknown, and New's error
// names the field.
func TestRun_moduleBackendFieldErrors(t *testing.T) {
	t.Parallel()

	recorder := &touchRecorder{mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}}

	backends := buildreg.Core()
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
			"an error of New", "data = 'x.wasm'\n",
			`components.fast.build: module is required for kind "module"`,
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
		Name:   "hooker",
		Kinds:  []string{sdkbuild.KindModule},
		Config: func() any { return new(struct{}) },
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

	backends := buildreg.Core()
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

		backends := buildreg.Core()
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
	assert.Equal(t, map[string][]string{"tool": {"tool"}}, result.Executables)
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
