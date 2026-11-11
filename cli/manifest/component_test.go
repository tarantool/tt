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
ldflags = ["-s", "-X", "main.version=1.0"]
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
	assert.Equal(t, []string{"-s", "-X", "main.version=1.0"}, gateway.Build.Ldflags)
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
			name: "bin on a cargo module",
			body: "[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"cargo\"\n" +
				"bin = \"a\"\n",
			field: "components.a.build.bin",
			msg:   "names the binary of an executable component",
		},
		{
			name: "module on a cargo program",
			body: "[components.a]\npath = \"a\"\nkind = \"executable\"\n" +
				"[components.a.build]\nbackend = \"cargo\"\nmodule = \"a\"\n",
			field: "components.a.build.module",
			msg:   "names the library of a module component",
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
		"cargo for a module": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"cargo\"\n",
		"cargo for a module with its name": "[components.fast]\npath = \"fast\"\n" +
			"[components.fast.build]\nbackend = \"cargo\"\nmodule = \"fast_rs\"\n" +
			"package = \"fast\"\nfeatures = [\"simd\"]\nprofile = \"dev\"\n",
		// module of c and lua-c on a make build was never checked either.
		"a stray module on make": "[components.lua]\npath = \"src\"\n" +
			"[components.lua.build]\nbackend = \"make\"\nmake_target = \"all\"\n" +
			"module = \"x\"\n",
		// A field of make on a shell build was never checked, and a manifest
		// that has one keeps building.
		"a stray make field on shell": "[components.lua]\npath = \"src\"\n" +
			"[components.lua.build]\nbackend = \"shell\"\ncommand = \"./b.sh\"\n" +
			"make_target = \"x\"\n",
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
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseBody(t, body).Validate()
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

			_, err := mfst.Validate()
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

	_, err := mfst.Validate()
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

	_, err := mfst.Validate()
	require.NoError(t, err)

	memcached := mfst.Components["mc"].Build
	assert.Equal(t, map[string]string{
		"ENABLE_BUNDLED_SMALL": "ON", "WITH_SASL:BOOL": "OFF", "CMAKE_PREFIX_PATH": "/deps",
	}, memcached.Variables)
	assert.Equal(t, []string{"internalso"}, memcached.Targets)
	assert.Equal(t, "Release", memcached.BuildType)
	assert.Equal(t, []string{"memcached/internal.so"}, memcached.Output)

	tools := mfst.Components["tools"]
	assert.True(t, tools.IsExecutable())
	assert.Equal(t, []string{"mc-bench", "mc-cli"}, tools.Build.Executables("tools"))

	component, found := mfst.ExecutableComponent("mc-bench")
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

			_, err := parseBody(t, tt.body).Validate()
			require.Error(t, err)

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, tt.field, verr.Field, err.Error())
			assert.Contains(t, verr.Msg, tt.msg)
		})
	}
}
