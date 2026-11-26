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
		{
			"an env of a variable of the script", shell(map[string]string{"tt_stage": "/"}), "gen",
			`the component "gen": components.gen.build.env: the script that builds the ` +
				`package without tt keeps a value of its own in the variable: tt_stage`,
		},
		{
			"an env of IFS", shell(map[string]string{"IFS": ":"}), "gen",
			`the component "gen": components.gen.build.env: the variable changes how sh runs ` +
				`the script that builds the package without tt: IFS`,
		},
		{
			"an env of a NUL byte", shell(map[string]string{"OK": "1", "NUL": "a\x00b"}), "gen",
			`the component "gen": components.gen.build.env: the value of NUL holds a NUL byte, ` +
				`which no script of sh holds`,
		},
		{
			"an argument of a NUL byte",
			manifest.Build{
				Backend: "shell", Cwd: "", Env: nil, Incremental: nil, Static: nil, Output: nil,
				Rest: map[string]any{"command": "sh", "args": []any{"-c", "true\x00; touch PWNED"}},
			},
			"gen", `the component "gen" is built by the tt backend "shell" with tt alone: the ` +
				`script of its export holds a NUL byte, which no script of sh holds`,
		},
		{
			"an output of a NUL byte",
			manifest.Build{
				Backend: "shell", Cwd: "", Env: nil, Incremental: nil, Static: nil,
				Output: manifest.OutputList{{Path: "out/x\x00.so", To: manifest.OutputToLib}},
				Rest:   map[string]any{"command": "true"},
			},
			"gen", `the component "gen" is built by the tt backend "shell" with tt alone: a ` +
				`file of its output holds a NUL byte, which no script of sh holds`,
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

// TestReleaseBuildOfANulByte: a package whose script would hold a NUL byte,
// one that slips by the checks of each build - in the name of the package
// here, which the manifest checks - builds with tt alone, saying so.
func TestReleaseBuildOfANulByte(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // The package and its hook alone matter.
	built := &build.Result{
		Manifest: &manifest.Manifest{
			Package: manifest.Package{Name: "de\x00mo"},
			Hooks: map[string]manifest.Hook{"pre_build": {
				Backend: "shell", Cwd: "", Env: nil, Incremental: nil, Static: nil, Output: nil,
				Rest: map[string]any{"command": "true"},
			}},
		},
		Product: manifest.ImplicitProduct,
	}

	//nolint:exhaustruct // The name and the version alone matter.
	plan, err := releaseBuild(built, rocktree.Release{Name: "demo", Version: "1.0.0"},
		rockSources{dir: t.TempDir(), carried: nil, backends: buildregtest.Registry()})
	require.NoError(t, err)

	assert.Equal(t, []string{"tt-rock.sh, the script that builds the package without tt, " +
		"holds a NUL byte, which no script of sh holds"}, plan.ttOnly)
	assert.Empty(t, plan.build.ExtraFiles)
	assert.Contains(t, plan.build.TtOnly, "demo 1.0.0 builds with tt only: tt-rock.sh")
}

// TestScriptedBuildLaysOutOnce: each file the layout of tt takes is put in
// the rock once - by build.install when the sources hold it, the script
// lays it out otherwise - and a file of the layout that is a file of the
// output, copied there by the script already, is left to that copy. A file
// of the layout of the build that the sources do not hold and no output
// lists is left out: the pack warns of it, as of a file git ignores when it
// built nothing, and naming the output to list it in when it built the
// package.
func TestScriptedBuildLaysOutOnce(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // The layout alone matters.
	built := &build.Result{
		Manifest: &manifest.Manifest{
			Package: manifest.Package{Name: "demo"},
			Components: map[string]manifest.Component{"native": {
				Path: "native", Build: &manifest.Build{Backend: "shell"},
			}},
		},
		Product: manifest.ImplicitProduct,
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

	sources := rockSources{
		dir: t.TempDir(), carried: []string{"prebuilt/x.so", "prebuilt/y.so", "native/v1.2/w.lua"},
		backends: buildregtest.Registry(), built: false,
	}

	plan := scriptedBuild(built, sources, script)

	assert.Equal(t, []rocktree.ReleaseFile{{Source: "prebuilt/x.so", Dest: "lib/demo/x.so"}},
		plan.build.Install)
	assert.Equal(t, rockScriptFile, plan.build.Script)
	assert.Equal(t, []string{"native/gen.lua"}, plan.leftOut, "what no build makes")
	assert.Equal(t, []string{"the source rock leaves out the files of the layout that git " +
		"ignores, or that are out of the project: native/gen.lua; a build of the source " +
		"rock, by tt or by LuaRocks, lacks them unless a build of the package makes them, " +
		"and one by LuaRocks unless the output of that build lists them, so commit them, or " +
		"leave them out of the layout"}, plan.warnings(nil))

	// The files of the output the layout takes, whether the project has them
	// or not, and no file a build made but those.
	rendered := plan.build.ExtraFiles[rockScriptFile]
	assert.Contains(t, rendered, "\ttt_lay prebuilt/y.so lib/demo/y.so\n"+
		"\ttt_lay native/v1.2/w.lua lua/demo/v1.2/w.lua\n"+
		"\ttt_lay native/out/y.so lib/demo/out/y.so\n"+
		"\ttt_lay native/out/z.so lib/demo/out/z.so\n}\n")
	assert.NotContains(t, rendered, "tt_lay native/fast.so")
	assert.NotContains(t, rendered, "gen.lua")

	sources.built = true

	again := scriptedBuild(built, sources, script)
	assert.Equal(t, plan.build, again.build, "the rockspec is the same")
	assert.Empty(t, again.leftOut)
	assert.Equal(t, []string{`the layout of the component "native" took native/gen.lua, ` +
		`which git does not track and no output lists: the rockspec leaves it out, and a ` +
		`build of the source rock by LuaRocks does not have it unless the output of ` +
		`[components.native.build] lists it`}, again.warnings(nil))
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
