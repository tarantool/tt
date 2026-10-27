package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
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

// TestExecutableComponentFields checks that every field of the go and cargo
// backends decodes into the build.
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
ldflags = ["-s", "-X main.version=1.0"]
cgo = true
static = true
env = { CC = "musl-gcc" }

[components.agent]
path = "agent"
kind = "executable"
[components.agent.build]
backend = "cargo"
bin = "agent-bin"
package = "agent-crate"
features = ["tls", "serde/derive"]
profile = "dist"
static = true
`)

	_, err := mfst.Validate()
	require.NoError(t, err)

	gateway := mfst.Components["gw"]
	assert.True(t, gateway.IsExecutable())
	assert.Equal(t, "./cmd/gw", gateway.Build.Package)
	assert.Equal(t, "gateway", gateway.Build.Name)
	assert.Equal(t, []string{"prod"}, gateway.Build.Tags)
	assert.Equal(t, []string{"-s", "-X main.version=1.0"}, gateway.Build.Ldflags)
	assert.True(t, gateway.Build.CgoValue())
	assert.True(t, gateway.Build.StaticValue())
	assert.Equal(t, []string{"gateway"}, gateway.Build.Executables("gw"))

	agent := mfst.Components["agent"]
	assert.Equal(t, "agent-bin", agent.Build.Bin)
	assert.Equal(t, "agent-crate", agent.Build.Package)
	assert.Equal(t, []string{"tls", "serde/derive"}, agent.Build.Features)
	assert.Equal(t, "dist", agent.Build.Profile)
	assert.True(t, agent.Build.StaticValue())
	assert.Equal(t, []string{"agent-bin"}, agent.Build.Executables("agent"))
}

func TestBuildExecutables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build manifest.Build
		want  []string
	}{
		{"go defaults to the component", manifest.Build{Backend: "go"}, []string{"gw"}},
		{"go name", manifest.Build{Backend: "go", Name: "gateway"}, []string{"gateway"}},
		{"cargo defaults to the component", manifest.Build{Backend: "cargo"}, []string{"gw"}},
		{"cargo bin", manifest.Build{Backend: "cargo", Bin: "gwd"}, []string{"gwd"}},
		{
			"make lists its output by base name",
			manifest.Build{Backend: "make", Output: []string{"out/gw", "out/gw-cli", "gw"}},
			[]string{"gw", "gw-cli"},
		},
		{"shell writing TT_OUTPUT_DIR names none", manifest.Build{Backend: "shell"}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.build.Executables("gw"))
		})
	}
}

func TestExecutableComponent(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.lua]
path = "src"

[components.gw]
path = "gw"
kind = "executable"
[components.gw.build]
backend = "go"
name = "gateway"

[components.tools]
path = "tools"
kind = "executable"
[components.tools.build]
backend = "make"
make_target = "all"
output = ["build/gw-cli"]
`)

	for _, tt := range []struct {
		binary    string
		component string
		found     bool
	}{
		{"gateway", "gw", true},
		{"gw", "gw", true},
		{"gw-cli", "tools", true},
		{"tools", "tools", true},
		{"lua", "", false},
		{"python3", "", false},
	} {
		component, found := mfst.ExecutableComponent(tt.binary)
		assert.Equal(t, tt.found, found, tt.binary)
		assert.Equal(t, tt.component, component, tt.binary)
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
			field: "components.a.build",
			msg:   `backend "c" builds a module`,
		},
		{
			name:  "go for a module",
			body:  "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"go\"\n",
			field: "components.a.build",
			msg:   `set kind = "executable"`,
		},
		{
			name:  "cargo for a module",
			body:  "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"cargo\"\n",
			field: "components.a.build",
			msg:   `set kind = "executable"`,
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
			name: "static on make",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"make\"\nmake_target = \"all\"\nstatic = true\n",
			field: "components.a.build.static",
			msg:   `is a field of backend go or cargo, not of "make"`,
		},
		{
			name: "static on shell",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\nstatic = false\n",
			field: "components.a.build.static",
			msg:   `not of "shell"`,
		},
		{
			name: "a field of go on a module build",
			body: "[components.a]\npath = \"a\"\n" +
				"[components.a.build]\nbackend = \"lua-c\"\nmodule = \"m\"\nsources = [\"m.c\"]\n" +
				"ldflags = [\"-s\"]\n",
			field: "components.a.build.ldflags",
			msg:   `not of "lua-c"`,
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
			body:  "[hooks.pre_build]\ncommand = \"./gen.sh\"\nstatic = true\n",
			field: "hooks.pre_build.static",
			msg:   `not of "shell"`,
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

			_, err := parseBody(t, tt.body).Validate()
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
		// A field of make on a shell build was never checked, and a manifest
		// that has one keeps building.
		"a stray make field on shell": "[components.lua]\npath = \"src\"\n" +
			"[components.lua.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
			"make_target = \"x\"\n",
		"a go program and a module of one name": "[components.gw]\npath = \"gw\"\n" +
			"kind = \"executable\"\n[components.gw.build]\nbackend = \"go\"\n" +
			"[components.lua]\npath = \"src\"\n[components.lua.build]\nbackend = \"make\"\n" +
			"make_target = \"all\"\noutput = [\"gw\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, body).Validate()
			require.NoError(t, err)
		})
	}
}
