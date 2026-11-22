package pack

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/rockscript"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// TestExportStep checks what makes a build export, of the backends of
// buildregtest: a backend of this tt that says its builds do, an Export
// that does not fail with ErrNoExport, a working directory in the project
// and an env of names of sh. The reason of each other build names it, and the
// field of the block an Export names.
func TestExportStep(t *testing.T) {
	t.Parallel()

	target := manifest.BuildTarget{
		Component: "gen", Kind: "", Path: "gen", Namespace: "demo", Package: "demo", Hook: false,
	}
	shell := func(env map[string]string) manifest.Build {
		return manifest.Build{
			Backend: "shell", Cwd: "", Env: env, Incremental: nil, Static: nil,
			Output: manifest.OutputList{{Path: "out/x.so", To: manifest.OutputToLib}},
			Rest:   map[string]any{"command": "sh", "args": []any{"-c", "true"}},
		}
	}
	locked := func(string) (string, bool) { return "", false }

	for _, test := range []struct {
		name   string
		block  manifest.Build
		cwd    string
		reason string
	}{
		{
			"a backend of no module",
			manifest.Build{Backend: "wasm", Cwd: "", Env: nil, Output: nil, Incremental: nil,
				Static: nil, Rest: nil},
			"gen", `the component "gen" is built by the tt backend "wasm", which no module of ` +
				`this tt provides`,
		},
		{
			"a backend of tt alone",
			manifest.Build{Backend: "exe", Cwd: "", Env: nil, Output: nil, Incremental: nil,
				Static: nil, Rest: nil},
			"gen", `the component "gen" is built by the tt backend "exe", which builds with tt ` +
				`alone`,
		},
		{
			"a field of tt alone",
			manifest.Build{
				Backend: "tree", Cwd: "", Env: nil, Incremental: nil, Static: nil,
				Output: manifest.OutputList{{Path: "x.json", To: manifest.OutputToShare}},
				Rest:   map[string]any{"variables": map[string]any{"X": "1"}},
			},
			"gen", `the component "gen" is built by the tt backend "tree" with tt alone, for ` +
				`components.gen.build.variables: the variables of a tree are set by tt alone`,
		},
		{
			"a directory out of the project", shell(nil), "../gen",
			`the component "gen" is built in ../gen, out of the project, which the sources ` +
				`of the rock do not hold`,
		},
		{
			"an env of no name of sh", shell(map[string]string{"OK": "1", "NOT-OK": "1"}), "gen",
			`the component "gen": components.gen.build.env: the variable has no name of sh: ` +
				`"NOT-OK"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, reason, err := exportStep(buildregtest.Registry(), `the component "gen"`,
				"components.gen.build", target, test.block, test.cwd, locked)
			require.NoError(t, err)
			assert.Equal(t, test.reason, reason)
		})
	}

	step, reason, err := exportStep(buildregtest.Registry(), `the component "gen"`,
		"components.gen.build", target, shell(map[string]string{"CFLAGS": "-O3"}), "./gen/",
		locked)
	require.NoError(t, err)
	assert.Empty(t, reason)
	assert.Equal(t, "gen", step.Cwd)
	assert.Equal(t, "shell", step.Backend)
	assert.Equal(t, map[string]string{"CFLAGS": "-O3"}, step.Env)
	assert.Equal(t, sdkbuild.Export{Script: "sh -c true", Tools: []string{"sh"}, Lost: nil},
		step.Export)
	assert.Equal(t, []sdkbuild.OutputFile{{Path: "out/x.so", To: sdkbuild.Module}}, step.Outputs)

	hook := manifest.BuildTarget{
		Component: "", Kind: "", Path: "", Namespace: "", Package: "demo", Hook: true,
	}

	step, reason, err = exportStep(buildregtest.Registry(), "the hook pre_build",
		"hooks.pre_build", hook, shell(nil), "", locked)
	require.NoError(t, err)
	assert.Empty(t, reason)
	assert.Empty(t, step.Cwd)
	assert.Empty(t, step.Outputs, "a hook has no output copied")
}

// TestScriptedBuildLaysOutOnce: each file the layout of tt takes is put in
// the rock once - by build.install when the sources hold it, the script
// lays it out otherwise - and a file of the layout that is a file of the
// output, copied there by the script already, is left to that copy.
func TestScriptedBuildLaysOutOnce(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // The layout alone matters.
	built := &build.Result{
		Manifest: &manifest.Manifest{Package: manifest.Package{Name: "demo"}},
		Product:  manifest.ImplicitProduct,
		Layout: []build.LaidOut{
			{Component: "native", Source: "native/fast.so", Dest: "lib/demo/fast.so"},
			{Component: "native", Source: "prebuilt/x.so", Dest: "lib/demo/x.so"},
			{Component: "native", Source: "prebuilt/y.so", Dest: "lib/demo/y.so"},
			{Component: "native", Source: "native/gen.lua", Dest: "lua/demo/gen.lua"},
			{Component: "native", Source: "native/out/z.so", Dest: "lib/demo/out/z.so"},
			{Component: "native", Source: "native/v1.2/w.lua", Dest: "lua/demo/v1.2/w.lua"},
		},
	}

	//nolint:exhaustruct // The outputs alone matter.
	script := rockscript.Script{
		Package: "demo", Version: "1.0.0",
		Components: []rockscript.Step{{
			Component: "native", Backend: "shell", Cwd: "native", Namespace: "demo",
			Outputs: []sdkbuild.OutputFile{
				{Path: "fast.so", To: sdkbuild.Module}, {Path: "out/y.so", To: sdkbuild.Module},
				{Path: "out/z.so", To: sdkbuild.Link},
			},
		}},
	}

	plan := scriptedBuild(built, rockSources{
		dir: t.TempDir(), carried: []string{"prebuilt/x.so", "prebuilt/y.so", "native/v1.2/w.lua"},
		backends: buildregtest.Registry(),
	}, script)

	assert.Equal(t, []rocktree.ReleaseFile{{Source: "prebuilt/x.so", Dest: "lib/demo/x.so"}},
		plan.build.Install)
	assert.Equal(t, rockScriptFile, plan.build.Script)
	assert.Equal(t, []string{"native/gen.lua"}, plan.leftOut, "what no build makes")
	assert.Equal(t, []string{"the source rock leaves out the files of the layout that git " +
		"ignores, or that are out of the project: native/gen.lua; a build of the source " +
		"rock, by tt or by LuaRocks, lacks them unless a build of the package makes them, " +
		"so commit them, or leave them out of the layout"}, plan.warnings(nil))

	rendered := plan.build.ExtraFiles[rockScriptFile]
	assert.Contains(t, rendered, "\ttt_lay prebuilt/y.so lib/demo/y.so\n"+
		"\ttt_lay native/gen.lua lua/demo/gen.lua\n"+
		"\ttt_lay native/out/z.so lib/demo/out/z.so\n"+
		"\ttt_lay native/v1.2/w.lua lua/demo/v1.2/w.lua\n}\n")
	assert.NotContains(t, rendered, "tt_lay native/fast.so")
}

// TestLockedVersions: a build is told the version of the closure of the
// product, else of the dev closure, as the build of tt is.
func TestLockedVersions(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // The closures alone matter.
	lock := &manifest.Lock{
		Products: map[string]manifest.LockProduct{"default": {
			Dependencies: []manifest.LockDependency{{Name: "a", Version: "1.0.0-1"}},
		}},
		DevDependencies: []manifest.LockDependency{
			{Name: "a", Version: "2.0.0-1"}, {Name: "b", Version: "3.0.0-1"},
		},
	}

	locked := lockedVersions(lock, "default")

	for rock, want := range map[string]string{"a": "1.0.0-1", "b": "3.0.0-1", "c": ""} {
		version, ok := locked(rock)
		assert.Equal(t, want, version, rock)
		assert.Equal(t, want != "", ok, rock)
	}

	_, ok := lockedVersions(nil, "default")("a")
	assert.False(t, ok)
}

// TestPackWarnsWhatTheScriptLacks: the pack of the source rock of a package
// of builds that export warns once of what each lacks against a build of
// tt, and the script says so in its comments.
func TestPackWarnsWhatTheScriptLacks(t *testing.T) {
	t.Parallel()

	src := writeGitPackage(t, map[string]string{
		manifestFileName: libraryManifest + `
[components.tree]
path = 'tree'

[components.tree.build]
backend = 'tree'
output = [{ path = 'data.json', to = 'share' }]
`,
		"tree/data.json": "{}\n",
	})

	var warnings []string

	opts := packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRockSrc)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	assert.Equal(t, []string{`LuaRocks builds the source rock by tt-rock.sh of its ` +
		`rockspec, which lacks what a build of tt has: the component "tree": the variables ` +
		`of a tree, which tt alone sets`}, warnings)

	table := buildTable(t, result.Rockspec)
	assert.Contains(t, table, "# Without tt, the component \"tree\", of the tt backend "+
		"\"tree\", lacks:\n#   - the variables of a tree, which tt alone sets\n")
	// tree copies its output itself, in its script; the script copies none.
	assert.Contains(t, table, "cp data.json \"$TT_SHARE_DIR\"/data.json\n")
	assert.NotContains(t, table, "tt_output data.json")
}
