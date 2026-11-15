package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// The build blocks of these tests are checked with the backends of the
// tests (buildregtest): make and shell, and the fakes exe, lib, both and
// tree. What the backends of tt check of their own fields their tests in
// modules/build check.

// backends returns the registry of the backends of the tests.
func backends() *buildreg.Registry {
	return buildregtest.Registry()
}

// parseBody parses a manifest made of the required header and body.
func parseBody(t *testing.T, body string) *manifest.Manifest {
	t.Helper()

	src := "manifest_version = \"0.1\"\n[package]\nname = \"demo\"\n" +
		"[platform]\ntarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n" + body

	mfst, _, err := manifest.ParseManifest([]byte(src))
	require.NoError(t, err)

	return mfst
}

// programsOf returns the programs the build block of the component name of
// mfst names to the checker of the backends of the tests.
func programsOf(t *testing.T, mfst *manifest.Manifest, name string) []string {
	t.Helper()

	require.NotNil(t, mfst.Components[name].Build)

	check, err := backends().CheckBuild("components."+name+".build",
		mfst.ComponentTarget(name), *mfst.Components[name].Build)
	require.NoError(t, err)

	return check.Programs
}

// TestBackendFieldsDecode checks that every field of a backend decodes into
// the Rest of the build, of a component of no kind and of one of the
// deprecated kind = "executable", and the common fields into theirs.
func TestBackendFieldsDecode(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.gw]
path = "gw"
kind = "executable"
[components.gw.build]
backend = "exe"
name = "gateway"
tags = ["prod"]
ldflags = ["-s", "-X", "main.version=1.0"]
cgo = true
static = true
env = { CC = "musl-gcc" }

[components.agent]
path = "agent"
[components.agent.build]
backend = "both"
bin = "agent-bin"
features = ["tls", "serde/derive"]
profile = "dist"
static = true
`)

	warnings, err := mfst.Validate(backends())
	require.NoError(t, err)
	assert.Equal(t, []string{"components.gw.kind is deprecated: a component makes what its " +
		"build makes; remove it"}, warnings)

	assert.True(t, mfst.Components["gw"].ProgramsOnly())
	assert.False(t, mfst.Components["agent"].ProgramsOnly())
	assert.Equal(t, []string{"gateway"}, programsOf(t, mfst, "gw"))

	gateway := mfst.Components["gw"].Build
	assert.Equal(t, map[string]any{
		"name":    "gateway",
		"tags":    []any{"prod"},
		"ldflags": []any{"-s", "-X", "main.version=1.0"},
		"cgo":     true,
	}, gateway.Rest)
	assert.True(t, gateway.StaticValue())
	assert.Equal(t, map[string]string{"CC": "musl-gcc"}, gateway.Env)

	assert.Equal(t, []string{"agent-bin"}, programsOf(t, mfst, "agent"))

	agent := mfst.Components["agent"].Build
	assert.Equal(t, map[string]any{
		"bin":      "agent-bin",
		"features": []any{"tls", "serde/derive"},
		"profile":  "dist",
	}, agent.Rest)
	assert.True(t, agent.StaticValue())
}

// TestBuildPrograms checks the programs the build of a component names to
// the checker, of no kind and of the deprecated kind = "executable".
func TestBuildPrograms(t *testing.T) {
	t.Parallel()

	target := manifest.BuildTarget{Component: "gw", Path: "gw", Package: "demo"}
	executable := target

	executable.Kind = manifest.KindExecutable

	tests := []struct {
		name   string
		target manifest.BuildTarget
		build  manifest.Build
		want   []string
	}{
		{"exe defaults to the component", target, manifest.Build{Backend: "exe"}, []string{"gw"}},
		{
			"exe name", target,
			manifest.Build{Backend: "exe", Rest: map[string]any{"name": "gateway"}},
			[]string{"gateway"},
		},
		{
			"both of kind = executable defaults to the component", executable,
			manifest.Build{Backend: "both"}, []string{"gw"},
		},
		{
			"both bin", target,
			manifest.Build{Backend: "both", Rest: map[string]any{"bin": "gwd"}},
			[]string{"gwd"},
		},
		{
			"both bin and module", target,
			manifest.Build{Backend: "both", Rest: map[string]any{"bin": "gwd", "module": "gw"}},
			[]string{"gwd"},
		},
		{
			"make of kind = executable lists its output by base name", executable,
			manifest.Build{
				Backend: "make", Output: []manifest.OutputEntry{
					{Path: "out/gw"}, {Path: "out/gw-cli"}, {Path: "gw"},
				},
				Rest: map[string]any{"make_target": "all"},
			},
			[]string{"gw", "gw-cli"},
		},
		{
			"make lists the programs of its output", target,
			manifest.Build{
				Backend: "make", Output: []manifest.OutputEntry{
					{Path: "out/gw.so"}, {Path: "out/gw", To: manifest.OutputToBin},
					{Path: "gw.wasm", To: manifest.OutputToShare},
				},
				Rest: map[string]any{"make_target": "all"},
			},
			[]string{"gw"},
		},
		{
			"shell writing TT_OUTPUT_DIR names none", executable,
			manifest.Build{Backend: "shell", Rest: map[string]any{"command": "./b.sh"}},
			[]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			check, err := backends().CheckBuild("components.gw.build", tt.target, tt.build)
			require.NoError(t, err)
			assert.Equal(t, tt.want, check.Programs)
		})
	}

	// A build of both that names neither builds a module, no program.
	check, err := backends().CheckBuild("components.gw.build", target,
		manifest.Build{Backend: "both"})
	require.NoError(t, err)
	assert.Nil(t, check.Programs)
}

func TestProgramComponent(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.lua]
path = "src"

[components.gw]
path = "gw"
[components.gw.build]
backend = "exe"
name = "gateway"

[components.tools]
path = "tools"
[components.tools.build]
backend = "make"
make_target = "all"
output = [{ path = "build/gw-cli", to = "bin" }, "build/tools.so"]

[components.legacy]
path = "legacy"
kind = "executable"
[components.legacy.build]
backend = "shell"
command = "./build.sh"

[components.writer]
path = "writer"
[components.writer.build]
backend = "shell"
command = "./build.sh"
`)

	recorded := map[string][]string{"writer": {"written"}, "lua": {"stale"}}

	for _, tt := range []struct {
		binary    string
		component string
		found     bool
	}{
		{"gateway", "gw", true},
		{"gw-cli", "tools", true},
		{"legacy", "legacy", true},
		{"written", "writer", true},
		// A component without a build makes nothing, whatever a record says.
		{"stale", "", false},
		// A component of no kind makes the programs its build names only.
		{"gw", "", false},
		{"tools", "", false},
		{"writer", "", false},
		{"lua", "", false},
		{"python3", "", false},
	} {
		component, found := mfst.ProgramComponent(backends(), tt.binary, recorded)
		assert.Equal(t, tt.found, found, tt.binary)
		assert.Equal(t, tt.component, component, tt.binary)
	}

	// With no checker and no record, a program is found by the name of a
	// component of kind = "executable" only.
	component, found := mfst.ProgramComponent(nil, "legacy", nil)
	assert.True(t, found)
	assert.Equal(t, "legacy", component)

	_, found = mfst.ProgramComponent(nil, "gateway", nil)
	assert.False(t, found)
}

// TestOutputEntries checks the two forms of an entry of output, and that the
// table form is written back as it was.
func TestOutputEntries(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.tool]
path = "tool"
[components.tool.build]
backend = "shell"
command = "./build.sh"
output = [
  "build/tool.so",
  { path = "build/tool", to = "bin" },
  { path = "build/tool.wasm", to = "share" },
  { path = "build/x.so", to = "lib" },
]

[components.other]
path = "other"
[components.other.build]
backend = "make"
make_target = "all"

[[components.other.build.output]]
path = "gen/other"
to = "bin"
`)

	_, err := mfst.Validate(backends())
	require.NoError(t, err)

	assert.Equal(t, manifest.OutputList{
		{Path: "build/tool.so"}, {Path: "build/tool", To: manifest.OutputToBin},
		{Path: "build/tool.wasm", To: manifest.OutputToShare},
		{Path: "build/x.so", To: manifest.OutputToLib},
	}, mfst.Components["tool"].Build.Output)
	assert.Equal(t, manifest.OutputList{{Path: "gen/other", To: manifest.OutputToBin}},
		mfst.Components["other"].Build.Output)
	assert.Equal(t, []string{"build/tool.so", "build/tool", "build/tool.wasm", "build/x.so"},
		manifest.OutputPaths(mfst.Components["tool"].Build.Output))
	assert.Equal(t, []string{"tool"}, programsOf(t, mfst, "tool"))

	data, err := mfst.Marshal()
	require.NoError(t, err)

	again, _, err := manifest.ParseManifest(data)
	require.NoError(t, err)
	assert.Equal(t, mfst.Components["tool"].Build.Output, again.Components["tool"].Build.Output)
}

// TestOutputAlone checks that an entry of output written alone, a string or
// a table, is the array of that entry: it means and hashes what the array
// does, the build block included.
func TestOutputAlone(t *testing.T) {
	t.Parallel()

	block := func(output string) *manifest.Manifest {
		return parseBody(t, "[components.calc]\npath = \"calc\"\n"+
			"[components.calc.build]\nbackend = \"shell\"\ncommand = \"./build.sh\"\n"+
			"output = "+output+"\n")
	}

	for _, tt := range []struct {
		alone, array string
		want         manifest.OutputList
	}{
		{`"dist/calc.wasm"`, `["dist/calc.wasm"]`, manifest.OutputList{{Path: "dist/calc.wasm"}}},
		{
			`{ path = "dist/calc.wasm", to = "share" }`,
			`[{ path = "dist/calc.wasm", to = "share" }]`,
			manifest.OutputList{{Path: "dist/calc.wasm", To: manifest.OutputToShare}},
		},
	} {
		alone, array := block(tt.alone), block(tt.array)

		_, err := alone.Validate(backends())
		require.NoError(t, err, tt.alone)
		assert.Equal(t, tt.want, alone.Components["calc"].Build.Output, tt.alone)
		assert.Equal(t, array.Components["calc"].Build.Output,
			alone.Components["calc"].Build.Output, tt.alone)

		aloneForm, err := alone.Components["calc"].Build.Canonical()
		require.NoError(t, err)

		arrayForm, err := array.Components["calc"].Build.Canonical()
		require.NoError(t, err)
		assert.Equal(t, string(arrayForm), string(aloneForm), tt.alone)
	}
}

// TestOutputEntryErrors covers the entries of output validation and parsing
// rule out.
func TestOutputEntryErrors(t *testing.T) {
	t.Parallel()

	head := "manifest_version = \"0.1\"\n[package]\nname = \"demo\"\n" +
		"[platform]\ntarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n" +
		"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"shell\"\n" +
		"command = \"./b.sh\"\n"

	for _, tt := range []struct {
		name, output, field, msg string
	}{
		{"a place of no name", `[{ path = "a", to = "etc" }]`, "components.a.build.output[0].to",
			`"etc" is not lib, share or bin`},
		{"no path", `["a", { to = "bin" }]`, "components.a.build.output[1].path", "is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mfst, _, err := manifest.ParseManifest([]byte(head + "output = " + tt.output + "\n"))
			require.NoError(t, err)

			_, err = mfst.Validate(nil)

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tt.field, verr.Field)
			assert.Equal(t, tt.msg, verr.Msg)
		})
	}

	for _, output := range []string{`[{ path = "a", into = "bin" }]`, `[1]`,
		`[{ path = "a", to = 1 }]`, `{ path = "a", into = "bin" }`} {
		_, _, err := manifest.ParseManifest([]byte(head + "output = " + output + "\n"))
		require.Error(t, err, output)
	}

	// A value of another type is told in the terms of the manifest, not of
	// Go.
	_, _, err := manifest.ParseManifest([]byte(head + "output = 1\n"))
	require.ErrorContains(t, err, "output must be a file, a table { path = ..., to = ... } "+
		"or an array of them, got integer")
	assert.NotContains(t, err.Error(), "OutputEntry")
}

// TestValidateComponentBuilds covers what an executable component, the kind
// of a component against what its build makes, the fields of one backend on
// the block of another and the errors a backend reports of its fields rule
// out.
func TestValidateComponentBuilds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		body  string
		field string
		msg   string
	}{
		{
			name:  "unknown kind",
			body:  "[components.a]\npath = \"a\"\nkind = \"library\"\n",
			field: "components.a.kind",
			msg:   `"library" is not supported`,
		},
		{
			name: "include",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\ninclude = [\"*.go\"]\n" +
				"[components.a.build]\nbackend = \"exe\"\n",
			field: "components.a.include",
			msg:   "is not allowed for kind",
		},
		{
			name: "exclude",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\nexclude = []\n" +
				"[components.a.build]\nbackend = \"exe\"\n",
			field: "components.a.exclude",
			msg:   "is not allowed for kind",
		},
		{
			name: "namespace",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\nnamespace = \"\"\n" +
				"[components.a.build]\nbackend = \"exe\"\n",
			field: "components.a.namespace",
			msg:   "is not allowed for kind",
		},
		{
			name:  "no build",
			body:  "[components.a]\npath = \"a\"\nkind = \"executable\"\n",
			field: "components.a.build",
			msg:   "is required for kind",
		},
		{
			name: "a module backend",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"lib\"\nmodule = \"m\"\nsources = [\"m.c\"]\n",
			field: "components.a.kind",
			msg:   `is "executable", but backend "lib" makes no programs`,
		},
		{
			name: "a backend of programs for kind = module",
			body: "[components.a]\npath = \"a\"\nkind = \"module\"\n" +
				"[components.a.build]\nbackend = \"exe\"\n",
			field: "components.a.kind",
			msg:   `is "module", but backend "exe" makes programs only`,
		},
		{
			name: "a build of a program of kind = module",
			body: "[components.a]\npath = \"a\"\nkind = \"module\"\n" +
				"[components.a.build]\nbackend = \"both\"\nbin = \"a\"\n",
			field: "components.a.kind",
			msg:   `is "module", but the build makes programs`,
		},
		{
			name: "a build of a module of kind = executable",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"both\"\nmodule = \"a\"\n",
			field: "components.a.kind",
			msg:   `is "executable", but the build makes modules`,
		},
		{
			name: "an output to lib of kind = executable",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
				"output = [\"gw\", { path = \"a.so\", to = \"lib\" }]\n",
			field: "components.a.kind",
			msg:   `is "executable", but the build makes modules`,
		},
		{
			name: "an output to bin of kind = module",
			body: "[components.a]\npath = \"a\"\nkind = \"module\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"output = [{ path = \"gw\", to = \"bin\" }]\n",
			field: "components.a.kind",
			msg:   `is "module", but the build makes programs`,
		},
		{
			name: "a program of make clashes with a program of exe",
			body: "[components.a]\npath = \"a\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"output = [\"a.so\", { path = \"out/b\", to = \"bin\" }]\n" +
				"[components.b]\npath = \"b\"\n[components.b.build]\nbackend = \"exe\"\n",
			field: "components.b.build",
			msg:   `program "b" is built by component "a" too`,
		},
		{
			name: "an error of a field of the backend",
			body: "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"both\"\n" +
				"module = \"a.b\"\n",
			field: "components.a.build.module",
			msg:   "is not a module name",
		},
		{
			name:  "an error of the block of the backend",
			body:  "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"tree\"\n",
			field: "components.a.build",
			msg:   `output is required for backend "tree"`,
		},
		{
			name: "module on exe",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"exe\"\nmodule = \"a\"\n",
			field: "components.a.build.module",
			msg:   `not of "exe"`,
		},
		{
			name: "a field of make on exe",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"exe\"\nmake_target = \"all\"\n",
			field: "components.a.build.make_target",
			msg:   `is a field of backend make, not of "exe"`,
		},
		{
			name: "output on both",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"both\"\noutput = [\"target/a\"]\n",
			field: "components.a.build.output",
			msg:   `not of "both"`,
		},
		{
			name: "a field of both on exe",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"exe\"\nfeatures = [\"x\"]\n",
			field: "components.a.build.features",
			msg:   `is a field of backend both, not of "exe"`,
		},
		{
			name: "a field of exe on both",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"both\"\ncgo = true\n",
			field: "components.a.build.cgo",
			msg:   `is a field of backend exe, not of "both"`,
		},
		{
			name: "a field of exe on make",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\ncgo = true\n",
			field: "components.a.build.cgo",
			msg:   `is a field of backend exe, not of "make"`,
		},
		{
			name: "module on make",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"module = \"x\"\n",
			field: "components.lua.build.module",
			msg:   `is a field of backend both or lib, not of "make"`,
		},
		{
			name: "a field of make on shell",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
				"make_target = \"x\"\n",
			field: "components.lua.build.make_target",
			msg:   `is a field of backend make, not of "shell"`,
		},
		{
			name: "a field of shell on make",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"args = [\"-j4\"]\n",
			field: "components.lua.build.args",
			msg:   `is a field of backend shell, not of "make"`,
		},
		{
			name: "a field of lib on shell",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
				"sources = [\"a.c\"]\n",
			field: "components.lua.build.sources",
			msg:   `is a field of backend lib, not of "shell"`,
		},
		{
			name: "a field of make on lib",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"lib\"\nmodule = \"m\"\n" +
				"sources = [\"m.c\"]\nflags = [\"-j4\"]\n",
			field: "components.lua.build.flags",
			msg:   `is a field of backend make, not of "lib"`,
		},
		{
			name: "output on lib",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"lib\"\nmodule = \"m\"\n" +
				"sources = [\"m.c\"]\noutput = [\"m.so\"]\n",
			field: "components.lua.build.output",
			msg:   `is a field of backend make, shell or tree, not of "lib"`,
		},
		{
			name: "incremental on tree",
			body: "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"tree\"\n" +
				"output = [\"a.so\"]\nincremental = true\n",
			field: "components.a.build.incremental",
			msg:   `is a field of backend make or shell, not of "tree"`,
		},
		{
			name: "a field of tree on make",
			body: "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"make\"\n" +
				"make_target = \"all\"\nvariables = { A = \"1\" }\n",
			field: "components.a.build.variables",
			msg:   `is a field of backend tree, not of "make"`,
		},
		{
			name:  "a field of make on a shell hook",
			body:  "[hooks.pre_build]\ncommand = \"./gen.sh\"\nentrypoint = \"Makefile\"\n",
			field: "hooks.pre_build.entrypoint",
			msg:   `is a field of backend make, not of "shell"`,
		},
		{
			name: "ldflags on both",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"both\"\nldflags = [\"-s\"]\n",
			field: "components.a.build.ldflags",
			msg:   `is a field of backend exe or lib, not of "both"`,
		},
		// A field of another backend is the block's by its key, whatever
		// its value: an empty one is as much a mistake.
		{
			name: "an empty field of make on exe",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"exe\"\nmake_target = \"\"\n",
			field: "components.a.build.make_target",
			msg:   `is a field of backend make, not of "exe"`,
		},
		{
			name: "an empty field of exe on shell",
			body: "[components.a]\npath = \"a\"\n" +
				"[components.a.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\nname = \"\"\n",
			field: "components.a.build.name",
			msg:   `is a field of backend exe, not of "shell"`,
		},
		{
			name: "two components build one program",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"exe\"\nname = \"gw\"\n" +
				"[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
				"[components.gw.build]\nbackend = \"both\"\n",
			field: "components.gw.build",
			msg:   `program "gw" is built by component "a" too`,
		},
		{
			name: "a make output clashes with a program of exe",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"output = [\"out/b\"]\n" +
				"[components.b]\npath = \"b\"\nkind = \"executable\"\n" +
				"[components.b.build]\nbackend = \"exe\"\n",
			field: "components.b.build",
			msg:   `program "b" is built by component "a" too`,
		},
		{
			name: "a tree program clashes with a program of exe",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"tree\"\noutput = [\"bin/b\"]\n" +
				"[components.b]\npath = \"b\"\nkind = \"executable\"\n" +
				"[components.b.build]\nbackend = \"exe\"\n",
			field: "components.b.build",
			msg:   `program "b" is built by component "a" too`,
		},
		{
			name:  "a field of exe on a hook",
			body:  "[hooks.pre_build]\ncommand = \"./gen.sh\"\ncgo = true\n",
			field: "hooks.pre_build.cgo",
			msg:   `not of "shell"`,
		},
		{
			name:  "static on a hook",
			body:  "[hooks.pre_build]\ncommand = \"./gen.sh\"\nstatic = true\n",
			field: "hooks.pre_build.static",
			msg:   "a hook makes nothing the build checks",
		},
		{
			name: "static = false on a make hook",
			body: "[hooks.post_build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"static = false\n",
			field: "hooks.post_build.static",
			msg:   "is for the build of a component",
		},
		{
			name:  "a backend of components for a hook",
			body:  "[hooks.pre_build]\nbackend = \"exe\"\n",
			field: "hooks.pre_build",
			msg:   `backend "exe" is not supported (want make or shell)`,
		},
		{
			name:  "tree for a hook",
			body:  "[hooks.pre_build]\nbackend = \"tree\"\noutput = [\"a\"]\n",
			field: "hooks.pre_build",
			msg:   `backend "tree" is not supported`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, tt.body).Validate(backends())
			require.Error(t, err)

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tt.field, verr.Field, err.Error())
			assert.Contains(t, verr.Msg, tt.msg)
		})
	}
}

// TestValidateComponentBuildsAccepted covers the builds of components
// validation lets through.
func TestValidateComponentBuildsAccepted(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"exe with defaults": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"exe\"\n",
		"both with defaults": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"both\"\n",
		"make writing TT_OUTPUT_DIR": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"make\"\nmake_target = \"gw\"\n",
		"shell with output": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"shell\"\ncommand = \"./build.sh\"\n" +
			"output = [\"out/gw\"]\n",
		"the module kind spelled out": "[components.lua]\npath = \"src\"\nkind = \"module\"\n",
		"both for a module": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"both\"\n",
		"both for a module with its name": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"both\"\nmodule = \"fast_rs\"\n" +
			"features = [\"simd\"]\nprofile = \"dev\"\n",
		"the environment of make": "[components.lua]\npath = \"src\"\n" +
			"[components.lua.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
			"env = { CC = \"clang\", CFLAGS = \"-O3\", CPPFLAGS = \"-DX\", LDFLAGS = \"-s\", " +
			"LIBS = \"-lm\", LDLIBS = \"-lz\" }\n",
		"a program and a module of one name": "[components.gw]\npath = \"gw\"\n" +
			"kind = \"executable\"\n[components.gw.build]\nbackend = \"exe\"\n" +
			"[components.lua]\npath = \"src\"\n[components.lua.build]\nbackend = \"make\"\n" +
			"make_target = \"all\"\noutput = [\"gw\"]\n",
		// The program of a build of one's own is checked as any is.
		"static on make": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"make\"\nmake_target = \"gw\"\nstatic = true\n",
		"static on shell": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\nstatic = false\n",
		"static on tree": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"tree\"\noutput = [\"gw\"]\nstatic = true\n",
		// A component of no kind makes what its build makes.
		"exe of no kind": "[components.gw]\npath = \"gw\"\n" +
			"[components.gw.build]\nbackend = \"exe\"\n",
		"exe with an include": "[components.gw]\npath = \"gw\"\ninclude = [\"lua/*.lua\"]\n" +
			"namespace = \"gw\"\n[components.gw.build]\nbackend = \"exe\"\n",
		"both of a module and a program": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"both\"\nmodule = \"fast\"\nbin = \"fastctl\"\n",
		"shell of the three": "[components.tool]\npath = \"tool\"\n" +
			"[components.tool.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
			"output = [\"t.so\", { path = \"t\", to = \"bin\" }, { path = \"t.wasm\", " +
			"to = \"share\" }]\n",
		"tree of the three": "[components.tool]\npath = \"tool\"\n" +
			"[components.tool.build]\nbackend = \"tree\"\n" +
			"output = [{ path = \"t.so\", to = \"lib\" }, { path = \"bin/t\", to = \"bin\" }, " +
			"{ path = \"t.wasm\", to = \"share\" }]\n",
		"an output to share of kind = module": "[components.tool]\npath = \"tool\"\n" +
			"kind = \"module\"\n[components.tool.build]\nbackend = \"shell\"\n" +
			"command = \"./b.sh\"\noutput = [{ path = \"t.wasm\", to = \"share\" }]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, body).Validate(backends())
			require.NoError(t, err)
		})
	}
}

// TestValidateStaticModules covers static on the build of a module
// component, of every sort of backend that builds one: the build checks the
// native files it made.
func TestValidateStaticModules(t *testing.T) {
	t.Parallel()

	for backend, fields := range map[string]string{
		"lib":   "module = \"m\"\nsources = [\"m.c\"]\n",
		"tree":  "output = [\"m.so\"]\n",
		"make":  "make_target = \"all\"\n",
		"shell": "command = \"./build.sh\"\n",
		"both":  "module = \"m\"\n",
	} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()

			mfst := parseBody(t, "[components.m]\npath = \"m\"\n[components.m.build]\n"+
				"backend = \""+backend+"\"\n"+fields+"static = true\n")

			_, err := mfst.Validate(backends())
			require.NoError(t, err)
			assert.True(t, mfst.Components["m"].Build.StaticValue())
		})
	}
}

// TestHookBackendDefaultsToShell checks that a hook naming no backend is a
// shell one once parsed, as the build runs it, and that one naming make keeps
// it.
func TestHookBackendDefaultsToShell(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, "[hooks.pre_build]\ncommand = \"./gen.sh\"\n"+
		"[hooks.post_build]\nbackend = \"make\"\nmake_target = \"post\"\n"+
		"[hooks.clean]\ncommand = \"rm\"\nargs = [\"-f\", \"gen.lua\"]\n")

	warnings, err := mfst.Validate(backends())
	require.NoError(t, err)
	assert.Empty(t, warnings, "clean is a hook, of tt package clean")
	assert.Equal(t, "shell", mfst.Hooks["pre_build"].Backend)
	assert.Equal(t, "make", mfst.Hooks["post_build"].Backend)
	assert.Equal(t, "shell", mfst.Hooks["clean"].Backend)

	// A tt with no backend that runs hooks, as a distribution without the
	// module of the standard backends, says so of a hook that names none.
	_, err = parseBody(t, "[hooks.pre_build]\ncommand = \"./gen.sh\"\n").
		Validate(buildreg.New())
	require.EqualError(t, err, `hooks.pre_build: backend "shell" is not supported: `+
		"no module of this tt provides a backend that runs hooks")
}

// TestBackendOfItsOutput checks the build of a backend that copies the
// files of output itself, of a module and of an executable component: its
// fields decode into Rest, output stays the block's, and the programs of an
// executable one are the files of its output.
func TestBackendOfItsOutput(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.mc]
path = "."
include = ["memcached/init.lua"]
[components.mc.build]
backend = "tree"
cwd = "."
variables = { ENABLE_BUNDLED_SMALL = "ON", "WITH_SASL:BOOL" = "OFF" }
targets = ["internalso"]
build_type = "Release"
output = ["memcached/internal.so"]
env = { CMAKE_GENERATOR = "Ninja" }

[components.tools]
path = "tools"
kind = "executable"
[components.tools.build]
backend = "tree"
output = ["bin/mc-bench", "mc-cli"]
`)

	_, err := mfst.Validate(backends())
	require.NoError(t, err)

	memcached := mfst.Components["mc"].Build
	assert.Equal(t, map[string]any{
		"variables":  map[string]any{"ENABLE_BUNDLED_SMALL": "ON", "WITH_SASL:BOOL": "OFF"},
		"targets":    []any{"internalso"},
		"build_type": "Release",
	}, memcached.Rest)
	assert.Equal(t, manifest.OutputList{{Path: "memcached/internal.so"}}, memcached.Output)

	tools := mfst.Components["tools"]
	assert.True(t, tools.ProgramsOnly())
	assert.Equal(t, []string{"mc-bench", "mc-cli"}, programsOf(t, mfst, "tools"))

	component, found := mfst.ProgramComponent(backends(), "mc-bench", nil)
	assert.True(t, found)
	assert.Equal(t, "tools", component)
}

// TestNestedBackendFields checks that the tables of the fields of a backend
// decode into Rest as they are, those of its platforms too.
func TestNestedBackendFields(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.memcached]
path = "memcached"
[components.memcached.build]
backend = "lib"
module = "datapath"
sources = ["datapath.c"]
cflags = ["-std=gnu99", "-Wall"]
ldflags = ["-Wl,-z,defs"]
env = { CPATH = "/opt/include" }
[components.memcached.build.platforms.linux]
ldflags = ["-Wl,--as-needed"]
[components.memcached.build.platforms.darwin]
cflags = ["-Wno-deprecated-declarations"]
ldflags = ["-Wl,-dead_strip"]
`)

	_, err := mfst.Validate(backends())
	require.NoError(t, err)

	build := mfst.Components["memcached"].Build
	assert.Equal(t, []any{"-std=gnu99", "-Wall"}, build.Rest["cflags"])
	assert.Equal(t, []any{"-Wl,-z,defs"}, build.Rest["ldflags"])
	assert.Equal(t, map[string]any{
		"linux": map[string]any{"ldflags": []any{"-Wl,--as-needed"}},
		"darwin": map[string]any{
			"cflags":  []any{"-Wno-deprecated-declarations"},
			"ldflags": []any{"-Wl,-dead_strip"},
		},
	}, build.Rest["platforms"])
	assert.Equal(t, map[string]string{"CPATH": "/opt/include"}, build.Env)

	// both has no platforms: a field a backend has no place for, and no
	// other backend has, is an unknown one.
	_, err = parseBody(t, "[components.a]\npath = \"a\"\n[components.a.build]\n"+
		"backend = \"both\"\nzz = 1\n").Validate(backends())
	require.EqualError(t, err, `components.a.build.zz: unknown field in manifest version "0.1"`)
}
