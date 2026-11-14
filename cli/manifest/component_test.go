package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
)

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
// mfst names to the checker of the backends of tt.
func programsOf(t *testing.T, mfst *manifest.Manifest, name string) []string {
	t.Helper()

	require.NotNil(t, mfst.Components[name].Build)

	check, err := buildreg.Core().CheckBuild("components."+name+".build",
		mfst.ComponentTarget(name), *mfst.Components[name].Build)
	require.NoError(t, err)

	return check.Programs
}

// TestExecutableComponentFields checks that every field of the go and cargo
// backends decodes into the build, of a component of no kind and of one of
// the deprecated kind = "executable".
func TestExecutableComponentFields(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.gw]
path = "gw"
kind = "executable"
[components.gw.build]
backend = "go"
package = "./cmd/gw"
name = "gateway"
tags = ["prod"]
ldflags = ["-s", "-X", "main.version=1.0"]
cgo = true
static = true
env = { CC = "musl-gcc" }

[components.agent]
path = "agent"
[components.agent.build]
backend = "cargo"
bin = "agent-bin"
package = "agent-crate"
features = ["tls", "serde/derive"]
profile = "dist"
static = true
`)

	warnings, err := mfst.Validate(buildreg.Core())
	require.NoError(t, err)
	assert.Equal(t, []string{"components.gw.kind is deprecated: a component makes what its " +
		"build makes; remove it"}, warnings)

	assert.True(t, mfst.Components["gw"].ProgramsOnly())
	assert.False(t, mfst.Components["agent"].ProgramsOnly())
	assert.Equal(t, []string{"gateway"}, programsOf(t, mfst, "gw"))

	gateway := mfst.Components["gw"].Build
	assert.Equal(t, map[string]any{
		"package": "./cmd/gw",
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
		"package":  "agent-crate",
		"features": []any{"tls", "serde/derive"},
		"profile":  "dist",
	}, agent.Rest)
	assert.True(t, agent.StaticValue())
}

// TestBuildPrograms checks the programs the build of a component names to
// the checker of the backends of tt, of no kind and of the deprecated
// kind = "executable".
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
		{"go defaults to the component", target, manifest.Build{Backend: "go"}, []string{"gw"}},
		{
			"go name", target,
			manifest.Build{Backend: "go", Rest: map[string]any{"name": "gateway"}},
			[]string{"gateway"},
		},
		{
			"cargo of kind = executable defaults to the component", executable,
			manifest.Build{Backend: "cargo"}, []string{"gw"},
		},
		{
			"cargo bin", target,
			manifest.Build{Backend: "cargo", Rest: map[string]any{"bin": "gwd"}},
			[]string{"gwd"},
		},
		{
			"cargo bin and module", target,
			manifest.Build{Backend: "cargo", Rest: map[string]any{"bin": "gwd", "module": "gw"}},
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

			check, err := buildreg.Core().CheckBuild("components.gw.build", tt.target, tt.build)
			require.NoError(t, err)
			assert.Equal(t, tt.want, check.Programs)
		})
	}

	// A cargo build that names neither builds the library, no program.
	check, err := buildreg.Core().CheckBuild("components.gw.build", target,
		manifest.Build{Backend: "cargo"})
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
backend = "go"
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
		component, found := mfst.ProgramComponent(buildreg.Core(), tt.binary, recorded)
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

	_, err := mfst.Validate(buildreg.Core())
	require.NoError(t, err)

	assert.Equal(t, []manifest.OutputEntry{
		{Path: "build/tool.so"}, {Path: "build/tool", To: manifest.OutputToBin},
		{Path: "build/tool.wasm", To: manifest.OutputToShare},
		{Path: "build/x.so", To: manifest.OutputToLib},
	}, mfst.Components["tool"].Build.Output)
	assert.Equal(t, []manifest.OutputEntry{{Path: "gen/other", To: manifest.OutputToBin}},
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
		`[{ path = "a", to = 1 }]`} {
		_, _, err := manifest.ParseManifest([]byte(head + "output = " + output + "\n"))
		require.Error(t, err, output)
	}
}

// TestValidateExecutableComponents covers what an executable component and
// the go and cargo backends rule out.
func TestValidateExecutableComponents(t *testing.T) {
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
				"[components.a.build]\nbackend = \"go\"\n",
			field: "components.a.include",
			msg:   "is not allowed for kind",
		},
		{
			name: "exclude",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\nexclude = []\n" +
				"[components.a.build]\nbackend = \"go\"\n",
			field: "components.a.exclude",
			msg:   "is not allowed for kind",
		},
		{
			name: "namespace",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\nnamespace = \"\"\n" +
				"[components.a.build]\nbackend = \"go\"\n",
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
				"[components.a.build]\nbackend = \"c\"\nmodule = \"m\"\nsources = [\"m.c\"]\n",
			field: "components.a.kind",
			msg:   `is "executable", but backend "c" makes no programs`,
		},
		{
			name: "go for kind = module",
			body: "[components.a]\npath = \"a\"\nkind = \"module\"\n" +
				"[components.a.build]\nbackend = \"go\"\n",
			field: "components.a.kind",
			msg:   `is "module", but backend "go" makes programs only`,
		},
		{
			name: "bin on a cargo build of kind = module",
			body: "[components.a]\npath = \"a\"\nkind = \"module\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\nbin = \"a\"\n",
			field: "components.a.kind",
			msg:   `is "module", but the build makes programs`,
		},
		{
			name: "module on a cargo build of kind = executable",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\nmodule = \"a\"\n",
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
			name: "a program of make clashes with a go program",
			body: "[components.a]\npath = \"a\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"output = [\"a.so\", { path = \"out/b\", to = \"bin\" }]\n" +
				"[components.b]\npath = \"b\"\n[components.b.build]\nbackend = \"go\"\n",
			field: "components.b.build",
			msg:   `program "b" is built by component "a" too`,
		},
		{
			name: "a dotted cargo module",
			body: "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"cargo\"\n" +
				"module = \"a.b\"\n",
			field: "components.a.build.module",
			msg:   "is not a module name",
		},
		{
			name: "module on go",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\nmodule = \"a\"\n",
			field: "components.a.build.module",
			msg:   `not of "go"`,
		},
		{
			name: "a field of make on go",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\nmake_target = \"all\"\n",
			field: "components.a.build.make_target",
			msg:   `is a field of backend make, not of "go"`,
		},
		{
			name: "output on cargo",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\noutput = [\"target/a\"]\n",
			field: "components.a.build.output",
			msg:   `not of "cargo"`,
		},
		{
			name: "a field of cargo on go",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\nfeatures = [\"x\"]\n",
			field: "components.a.build.features",
			msg:   `is a field of backend cargo, not of "go"`,
		},
		{
			name: "a field of go on cargo",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\ncgo = true\n",
			field: "components.a.build.cgo",
			msg:   `is a field of backend go, not of "cargo"`,
		},
		{
			name: "a field of go on make",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\ncgo = true\n",
			field: "components.a.build.cgo",
			msg:   `is a field of backend go, not of "make"`,
		},
		// A field of make, shell, c or lua-c on a build of another of the four
		// is refused as any other.
		{
			name: "module on make",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"module = \"x\"\n",
			field: "components.lua.build.module",
			msg:   `is a field of backend c, cargo or lua-c, not of "make"`,
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
			name: "a field of c on shell",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
				"sources = [\"a.c\"]\n",
			field: "components.lua.build.sources",
			msg:   `is a field of backend c or lua-c, not of "shell"`,
		},
		{
			name: "a field of make on lua-c",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"lua-c\"\nmodule = \"m\"\n" +
				"sources = [\"m.c\"]\nflags = [\"-j4\"]\n",
			field: "components.lua.build.flags",
			msg:   `is a field of backend make, not of "lua-c"`,
		},
		{
			name: "output on c",
			body: "[components.lua]\npath = \"src\"\n" +
				"[components.lua.build]\nbackend = \"c\"\nmodule = \"m\"\n" +
				"sources = [\"m.c\"]\noutput = [\"m.so\"]\n",
			field: "components.lua.build.output",
			msg:   `is a field of backend make, shell or cmake, not of "c"`,
		},
		{
			name:  "a field of make on a shell hook",
			body:  "[hooks.pre_build]\ncommand = \"./gen.sh\"\nentrypoint = \"Makefile\"\n",
			field: "hooks.pre_build.entrypoint",
			msg:   `is a field of backend make, not of "shell"`,
		},
		{
			name: "a field of go on a module build",
			body: "[components.a]\npath = \"a\"\n" +
				"[components.a.build]\nbackend = \"lua-c\"\nmodule = \"m\"\nsources = [\"m.c\"]\n" +
				"tags = [\"netgo\"]\n",
			field: "components.a.build.tags",
			msg:   `not of "lua-c"`,
		},
		{
			name: "ldflags on cargo",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\nldflags = [\"-s\"]\n",
			field: "components.a.build.ldflags",
			msg:   `is a field of backend c, go or lua-c, not of "cargo"`,
		},
		// A field of another backend is the block's by its key, whatever
		// its value: an empty one is as much a mistake.
		{
			name: "an empty field of make on go",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\nmake_target = \"\"\n",
			field: "components.a.build.make_target",
			msg:   `is a field of backend make, not of "go"`,
		},
		{
			name: "an empty field of go on shell",
			body: "[components.a]\npath = \"a\"\n" +
				"[components.a.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\nname = \"\"\n",
			field: "components.a.build.name",
			msg:   `is a field of backend go, not of "shell"`,
		},
		{
			name: "go name with a slash",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\nname = \"bin/gw\"\n",
			field: "components.a.build.name",
			msg:   "is not a file name",
		},
		{
			name: "cargo bin of a dot",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\nbin = \"..\"\n",
			field: "components.a.build.bin",
			msg:   "is not a file name",
		},
		{
			name: "two go tags in one",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\ntags = [\"netgo,osusergo\"]\n",
			field: "components.a.build.tags",
			msg:   "is not a build tag",
		},
		{
			name: "an empty ldflag",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\nldflags = [\"-s\", \"\"]\n",
			field: "components.a.build.ldflags",
			msg:   "empty entry",
		},
		{
			name: "a cargo profile with a space",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\nprofile = \"my profile\"\n",
			field: "components.a.build.profile",
			msg:   "is not a profile name",
		},
		{
			name: "two components build one program",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\nname = \"gw\"\n" +
				"[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
				"[components.gw.build]\nbackend = \"cargo\"\n",
			field: "components.gw.build",
			msg:   `program "gw" is built by component "a" too`,
		},
		{
			name: "a make output clashes with a go program",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
				"output = [\"out/b\"]\n" +
				"[components.b]\npath = \"b\"\nkind = \"executable\"\n" +
				"[components.b.build]\nbackend = \"go\"\n",
			field: "components.b.build",
			msg:   `program "b" is built by component "a" too`,
		},
		{
			name:  "a go field on a hook",
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
			name:  "go for a hook",
			body:  "[hooks.pre_build]\nbackend = \"go\"\n",
			field: "hooks.pre_build",
			msg:   `backend "go" is not supported`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, tt.body).Validate(buildreg.Core())
			require.Error(t, err)

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tt.field, verr.Field, err.Error())
			assert.Contains(t, verr.Msg, tt.msg)
		})
	}
}

// TestValidateExecutableComponentsAccepted covers the executable components
// validation lets through.
func TestValidateExecutableComponentsAccepted(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"go with defaults": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"go\"\n",
		"cargo with defaults": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"cargo\"\n",
		"make writing TT_OUTPUT_DIR": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"make\"\nmake_target = \"gw\"\n",
		"shell with output": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"shell\"\ncommand = \"./build.sh\"\n" +
			"output = [\"out/gw\"]\n",
		"the module kind spelled out": "[components.lua]\npath = \"src\"\nkind = \"module\"\n",
		"cargo for a module": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"cargo\"\n",
		"cargo for a module with its name": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"cargo\"\nmodule = \"fast_rs\"\n" +
			"package = \"fast\"\nfeatures = [\"simd\"]\nprofile = \"dev\"\n",
		// make reads CFLAGS of its environment, unlike the cc of c and lua-c.
		"CFLAGS in env of make": "[components.lua]\npath = \"src\"\n" +
			"[components.lua.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
			"env = { CC = \"clang\", CFLAGS = \"-O3\", CPPFLAGS = \"-DX\", LDFLAGS = \"-s\", " +
			"LIBS = \"-lm\", LDLIBS = \"-lz\" }\n",
		"a go program and a module of one name": "[components.gw]\npath = \"gw\"\n" +
			"kind = \"executable\"\n[components.gw.build]\nbackend = \"go\"\n" +
			"[components.lua]\npath = \"src\"\n[components.lua.build]\nbackend = \"make\"\n" +
			"make_target = \"all\"\noutput = [\"gw\"]\n",
		// The program of a build of one's own is checked as one of go is.
		"static on make": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"make\"\nmake_target = \"gw\"\nstatic = true\n",
		"static on shell": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\nstatic = false\n",
		"static on cmake": "[components.gw]\npath = \"gw\"\nkind = \"executable\"\n" +
			"[components.gw.build]\nbackend = \"cmake\"\noutput = [\"gw\"]\nstatic = true\n",
		// A component of no kind makes what its build makes.
		"go of no kind": "[components.gw]\npath = \"gw\"\n" +
			"[components.gw.build]\nbackend = \"go\"\n",
		"go with an include": "[components.gw]\npath = \"gw\"\ninclude = [\"lua/*.lua\"]\n" +
			"namespace = \"gw\"\n[components.gw.build]\nbackend = \"go\"\n",
		"cargo of a module and a program": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"cargo\"\nmodule = \"fast\"\nbin = \"fastctl\"\n",
		"shell of the three": "[components.tool]\npath = \"tool\"\n" +
			"[components.tool.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
			"output = [\"t.so\", { path = \"t\", to = \"bin\" }, { path = \"t.wasm\", " +
			"to = \"share\" }]\n",
		"cmake of the three": "[components.tool]\npath = \"tool\"\n" +
			"[components.tool.build]\nbackend = \"cmake\"\n" +
			"output = [{ path = \"t.so\", to = \"lib\" }, { path = \"bin/t\", to = \"bin\" }, " +
			"{ path = \"t.wasm\", to = \"share\" }]\n",
		"an output to share of kind = module": "[components.tool]\npath = \"tool\"\n" +
			"kind = \"module\"\n[components.tool.build]\nbackend = \"shell\"\n" +
			"command = \"./b.sh\"\noutput = [{ path = \"t.wasm\", to = \"share\" }]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, body).Validate(buildreg.Core())
			require.NoError(t, err)
		})
	}
}

// TestValidateStaticModules covers static on the build of a module
// component, of every backend that builds one: the build checks the native
// files it made.
func TestValidateStaticModules(t *testing.T) {
	t.Parallel()

	for backend, fields := range map[string]string{
		"c":     "module = \"m\"\nsources = [\"m.c\"]\n",
		"lua-c": "module = \"m\"\nsources = [\"m.c\"]\n",
		"cmake": "output = [\"m.so\"]\n",
		"make":  "make_target = \"all\"\n",
		"shell": "command = \"./build.sh\"\n",
		"cargo": "module = \"m\"\n",
	} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()

			mfst := parseBody(t, "[components.m]\npath = \"m\"\n[components.m.build]\n"+
				"backend = \""+backend+"\"\n"+fields+"static = true\n")

			_, err := mfst.Validate(buildreg.Core())
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
		"[hooks.post_build]\nbackend = \"make\"\nmake_target = \"post\"\n")

	_, err := mfst.Validate(buildreg.Core())
	require.NoError(t, err)
	assert.Equal(t, "shell", mfst.Hooks["pre_build"].Backend)
	assert.Equal(t, "make", mfst.Hooks["post_build"].Backend)
}

// TestCMakeComponentFields checks that every field of the cmake backend
// decodes into the build, of a module and of an executable component.
func TestCMakeComponentFields(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.mc]
path = "."
include = ["memcached/init.lua"]
[components.mc.build]
backend = "cmake"
cwd = "."
variables = { ENABLE_BUNDLED_SMALL = "ON", "WITH_SASL:BOOL" = "OFF", CMAKE_PREFIX_PATH = "/deps" }
targets = ["internalso"]
build_type = "Release"
output = ["memcached/internal.so"]
env = { CMAKE_GENERATOR = "Ninja" }

[components.tools]
path = "tools"
kind = "executable"
[components.tools.build]
backend = "cmake"
output = ["bin/mc-bench", "mc-cli"]
`)

	_, err := mfst.Validate(buildreg.Core())
	require.NoError(t, err)

	memcached := mfst.Components["mc"].Build
	assert.Equal(t, map[string]any{
		"variables": map[string]any{
			"ENABLE_BUNDLED_SMALL": "ON", "WITH_SASL:BOOL": "OFF", "CMAKE_PREFIX_PATH": "/deps",
		},
		"targets":    []any{"internalso"},
		"build_type": "Release",
	}, memcached.Rest)
	assert.Equal(t, []manifest.OutputEntry{{Path: "memcached/internal.so"}}, memcached.Output)

	tools := mfst.Components["tools"]
	assert.True(t, tools.ProgramsOnly())
	assert.Equal(t, []string{"mc-bench", "mc-cli"}, programsOf(t, mfst, "tools"))

	component, found := mfst.ProgramComponent(buildreg.Core(), "mc-bench", nil)
	assert.True(t, found)
	assert.Equal(t, "tools", component)
}

// TestValidateCMakeBuild covers what the cmake backend rules out, and the
// fields of cmake on the builds of other backends.
func TestValidateCMakeBuild(t *testing.T) {
	t.Parallel()

	module := "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"cmake\"\n"
	cmake := module + "output = [\"a.so\"]\n"

	tests := []struct {
		name  string
		body  string
		field string
		msg   string
	}{
		{"no output", module, "components.a.build", `output is required for backend "cmake"`},
		{
			"an empty output", module + "output = [\"a.so\", \"\"]\n",
			"components.a.build.output", "must not contain an empty entry",
		},
		{
			"an absolute output", module + "output = [\"/usr/lib/a.so\"]\n",
			"components.a.build.output", `"/usr/lib/a.so" is not relative to the build tree`,
		},
		{
			"a variable with =", cmake + "variables = { \"A=B\" = \"1\" }\n",
			"components.a.build.variables", `"A=B" is not a variable name`,
		},
		{
			"a variable with a space", cmake + "variables = { \"A B\" = \"1\" }\n",
			"components.a.build.variables", `"A B" is not a variable name`,
		},
		{
			"a variable with a lowercase type", cmake + "variables = { \"A:bool\" = \"1\" }\n",
			"components.a.build.variables", `"A:bool" is not a variable name`,
		},
		{
			"the build type as a variable",
			cmake + "variables = { CMAKE_BUILD_TYPE = \"Debug\" }\n",
			"components.a.build.variables", "CMAKE_BUILD_TYPE is set by build_type",
		},
		{
			"the build type as a typed variable",
			cmake + "variables = { \"CMAKE_BUILD_TYPE:STRING\" = \"Debug\" }\n",
			"components.a.build.variables", "CMAKE_BUILD_TYPE is set by build_type",
		},
		{
			"a target with a space", cmake + "targets = [\"a b\"]\n",
			"components.a.build.targets", `"a b" is not a target name`,
		},
		{
			"a target like an option", cmake + "targets = [\"--clean-first\"]\n",
			"components.a.build.targets", `"--clean-first" is not a target name`,
		},
		{
			"an empty target", cmake + "targets = [\"\"]\n",
			"components.a.build.targets", `"" is not a target name`,
		},
		{
			"a build type with a space", cmake + "build_type = \"Rel With\"\n",
			"components.a.build.build_type", `"Rel With" is not a build type`,
		},
		{
			"a field of make on cmake", cmake + "make_target = \"all\"\n",
			"components.a.build.make_target", `is a field of backend make, not of "cmake"`,
		},
		{
			"incremental on cmake", cmake + "incremental = true\n",
			"components.a.build.incremental",
			`is a field of backend make or shell, not of "cmake"`,
		},
		{
			"module on cmake", cmake + "module = \"a\"\n",
			"components.a.build.module", `not of "cmake"`,
		},
		{
			"defines on cmake", cmake + "defines = [\"A=1\"]\n",
			"components.a.build.defines", `is a field of backend c or lua-c, not of "cmake"`,
		},
		{
			"variables on make",
			"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"make\"\n" +
				"make_target = \"all\"\nvariables = { A = \"1\" }\n",
			"components.a.build.variables", `is a field of backend cmake, not of "make"`,
		},
		{
			"targets on lua-c",
			"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"lua-c\"\n" +
				"module = \"m\"\nsources = [\"m.c\"]\ntargets = [\"m\"]\n",
			"components.a.build.targets", `is a field of backend cmake, not of "lua-c"`,
		},
		{
			"build_type on cargo",
			"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"cargo\"\n" +
				"build_type = \"Release\"\n",
			"components.a.build.build_type", `is a field of backend cmake, not of "cargo"`,
		},
		{
			"a cmake program clashes with a go program",
			"[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cmake\"\noutput = [\"bin/b\"]\n" +
				"[components.b]\npath = \"b\"\nkind = \"executable\"\n" +
				"[components.b.build]\nbackend = \"go\"\n",
			"components.b.build", `program "b" is built by component "a" too`,
		},
		{
			"cmake for a hook", "[hooks.pre_build]\nbackend = \"cmake\"\noutput = [\"a\"]\n",
			"hooks.pre_build", `backend "cmake" is not supported`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, tt.body).Validate(buildreg.Core())
			require.Error(t, err)

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tt.field, verr.Field, err.Error())
			assert.Contains(t, verr.Msg, tt.msg)
		})
	}
}

// TestCcComponentFields checks that the flags of the compiler and of the
// linker of a c or lua-c build decode into the build and into its overlays
// of platforms.
func TestCcComponentFields(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.memcached]
path = "memcached"
[components.memcached.build]
backend = "lua-c"
module = "datapath"
sources = ["datapath.c"]
cflags = ["-std=gnu99", "-Wall", "-Wextra", "-Werror"]
ldflags = ["-Wl,-z,defs"]
env = { CPATH = "/opt/include" }
[components.memcached.build.platforms.linux]
ldflags = ["-Wl,--as-needed"]
[components.memcached.build.platforms.darwin]
cflags = ["-Wno-deprecated-declarations"]
ldflags = ["-Wl,-dead_strip"]
`)

	_, err := mfst.Validate(buildreg.Core())
	require.NoError(t, err)

	build := mfst.Components["memcached"].Build
	assert.Equal(t, []any{"-std=gnu99", "-Wall", "-Wextra", "-Werror"}, build.Rest["cflags"])
	assert.Equal(t, []any{"-Wl,-z,defs"}, build.Rest["ldflags"])
	assert.Equal(t, map[string]any{
		"linux": map[string]any{"ldflags": []any{"-Wl,--as-needed"}},
		"darwin": map[string]any{
			"cflags":  []any{"-Wno-deprecated-declarations"},
			"ldflags": []any{"-Wl,-dead_strip"},
		},
	}, build.Rest["platforms"])
	assert.Equal(t, map[string]string{"CPATH": "/opt/include"}, build.Env)
}

// TestValidateCcFlags covers what validation rules out of the flags of a c
// or lua-c build, and cflags on the build of another backend.
func TestValidateCcFlags(t *testing.T) {
	t.Parallel()

	module := "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"c\"\n" +
		"module = \"m\"\nsources = [\"m.c\"]\n"

	tests := []struct {
		name  string
		body  string
		field string
		msg   string
	}{
		{
			"an empty cflag", module + "cflags = [\"-Wall\", \"\"]\n",
			"components.a.build.cflags", "must not contain an empty entry",
		},
		{
			"an empty ldflag", module + "ldflags = [\"\"]\n",
			"components.a.build.ldflags", "must not contain an empty entry",
		},
		{
			"an empty cflag of an overlay",
			module + "[components.a.build.platforms.linux]\ncflags = [\"\"]\n",
			"components.a.build.platforms.linux.cflags", "must not contain an empty entry",
		},
		{
			"an empty ldflag of an overlay",
			module + "[components.a.build.platforms.darwin]\nldflags = [\"-Wl,-x\", \"\"]\n",
			"components.a.build.platforms.darwin.ldflags", "must not contain an empty entry",
		},
		{
			"cflags on cmake",
			"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"cmake\"\n" +
				"output = [\"a.so\"]\ncflags = [\"-O3\"]\n",
			"components.a.build.cflags", `is a field of backend c or lua-c, not of "cmake"`,
		},
		{
			"cflags on go",
			"[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"go\"\ncflags = [\"-O3\"]\n",
			"components.a.build.cflags", `is a field of backend c or lua-c, not of "go"`,
		},
		{
			"cflags on make",
			"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"make\"\n" +
				"make_target = \"all\"\ncflags = [\"-O3\"]\n",
			"components.a.build.cflags", `is a field of backend c or lua-c, not of "make"`,
		},
		{
			"ldflags on shell",
			"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"shell\"\n" +
				"command = \"./b.sh\"\nldflags = [\"-s\"]\n",
			"components.a.build.ldflags", `is a field of backend c, go or lua-c, not of "shell"`,
		},
		{
			"CC in env", module + "env = { CC = \"clang\" }\n",
			"components.a.build.env.CC", "the compiler is CC of the environment tt runs in",
		},
		{
			"CFLAGS in env", module + "env = { CFLAGS = \"-O0 -g\" }\n",
			"components.a.build.env.CFLAGS", "put the flags of the compiler into cflags",
		},
		{
			"LDFLAGS in env of lua-c",
			"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"lua-c\"\n" +
				"module = \"m\"\nsources = [\"m.c\"]\n" +
				"env = { LDFLAGS = \"-s\", PATH = \"/bin\" }\n",
			"components.a.build.env.LDFLAGS", "put the flags of the link into ldflags",
		},
		{
			"CPPFLAGS in env", module + "env = { CPPFLAGS = \"-DNDEBUG -I/opt/inc\" }\n",
			"components.a.build.env.CPPFLAGS", "put the macros into defines",
		},
		{
			"LIBS in env", module + "env = { LIBS = \"-lsasl2\" }\n",
			"components.a.build.env.LIBS", "put the libraries into libraries",
		},
		{
			"LDLIBS in env", module + "env = { LDLIBS = \"-lm\" }\n",
			"components.a.build.env.LDLIBS", "put the libraries into libraries",
		},
		{
			"an overlay of an OS tt does not build for",
			module + "[components.a.build.platforms.macos]\ncflags = [\"-O3\"]\n",
			"components.a.build.platforms.macos",
			`"macos" is not an OS tt builds for (want linux or darwin)`,
		},
		{
			"an overlay of a platform, not an OS",
			module + "[components.a.build.platforms.linux-amd64]\ndefines = [\"X\"]\n",
			"components.a.build.platforms.linux-amd64", "is not an OS tt builds for",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, tt.body).Validate(buildreg.Core())
			require.Error(t, err)

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tt.field, verr.Field, err.Error())
			assert.Contains(t, verr.Msg, tt.msg)
		})
	}
}
