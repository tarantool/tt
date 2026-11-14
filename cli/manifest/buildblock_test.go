package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// TestBuildRest checks that the fields of the backend of a build block go to
// Rest, as the TOML decoder reads them, and the common ones do not.
func TestBuildRest(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.a]
path = "a"
[components.a.build]
backend = "lib"
cwd = "src"
module = "m"
sources = ["m.c"]
[components.a.build.platforms.linux]
cflags = ["-O2"]
[components.b]
path = "b"
[hooks.pre_build]
command = "./gen.sh"
args = ["x"]
`)

	assert.Equal(t, &manifest.Build{
		Backend: "lib", Cwd: "src", Env: nil, Output: nil, Incremental: nil, Static: nil,
		Rest: map[string]any{
			"module": "m", "sources": []any{"m.c"},
			"platforms": map[string]any{"linux": map[string]any{"cflags": []any{"-O2"}}},
		},
	}, mfst.Components["a"].Build)
	assert.Nil(t, mfst.Components["b"].Build)
	assert.Equal(t, map[string]any{"command": "./gen.sh", "args": []any{"x"}},
		mfst.Hooks["pre_build"].Rest)
}

// TestBuildRoundTrip checks that the fields of a backend are written back
// with those of the block.
func TestBuildRoundTrip(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, `
[components.a]
path = "a"
[components.a.build]
backend = "tree"
output = ["a.so"]
static = true
variables = { A = "1" }
[hooks.post_build]
backend = "make"
make_target = "post"
`)

	data, err := mfst.Marshal()
	require.NoError(t, err)

	again, _, err := manifest.ParseManifest(data)
	require.NoError(t, err)
	assert.Equal(t, mfst.Components, again.Components)
	assert.Equal(t, mfst.Hooks, again.Hooks)
	assert.Equal(t, mfst.Hash(), again.Hash(), "the written manifest says what the read one did")
}

// TestValidateBuildFieldsByVersion checks that a field of a build block no
// backend has is refused in a manifest of this version, as any unknown
// field, and is a warning in one of a newer minor version.
func TestValidateBuildFieldsByVersion(t *testing.T) {
	t.Parallel()

	body := "[package]\nname = \"demo\"\n[platform]\ntarantool = \">=3.0.0\"\n" +
		"tt = \">=3.0.0\"\n[components.a]\npath = \"a\"\n[components.a.build]\n" +
		"backend = \"make\"\nmake_target = \"all\"\njobs = 4\n" +
		"[hooks.pre_build]\ncommand = \"./gen.sh\"\nretries = 2\n"

	mfst, warnings, err := manifest.ParseManifest([]byte("manifest_version = \"0.1\"\n" + body))
	require.NoError(t, err, "the fields of a backend are not checked when parsed")
	assert.Empty(t, warnings)

	_, err = mfst.Validate(backends())
	require.EqualError(t, err, `components.a.build.jobs: unknown field in manifest version "0.1"`)

	mfst, _, err = manifest.ParseManifest([]byte("manifest_version = \"0.9\"\n" + body))
	require.NoError(t, err)

	warnings, err = mfst.Validate(backends())
	require.NoError(t, err)
	assert.Equal(t, []string{
		`field "components.a.build.jobs" appeared in a newer manifest version; ` +
			"trying to ignore it",
		`field "hooks.pre_build.retries" appeared in a newer manifest version; ` +
			"trying to ignore it",
	}, warnings)
}

// TestValidateBuildFields checks the errors of the fields of a build block
// the TOML decoder reported when the manifest was parsed.
func TestValidateBuildFields(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"lib\"\n" +
			"module = \"m\"\nsources = [\"m.c\"]\n[components.a.build.platforms.linux]\n" +
			"cflag = [\"-O2\"]\n": `components.a.build.platforms.linux.cflag: ` +
			`unknown field in manifest version "0.1"`,
		"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"lib\"\n" +
			"module = \"m\"\nsources = [\"m.c\"]\ncflags = \"-O2\"\n": "components.a.build." +
			"cflags: must be an array, not a string",
		"[components.a]\npath = \"a\"\n[components.a.build]\nbackend = \"lib\"\n" +
			"module = \"m\"\nsources = [\"m.c\", 1]\n": `components.a.build.sources[1]: ` +
			`must be a string, not an integer`,
		"[hooks.pre_build]\ncommand = \"./gen.sh\"\nargs = \"-x\"\n": `hooks.pre_build.args: ` +
			`must be an array, not a string`,
	} {
		_, err := parseBody(t, body).Validate(backends())
		require.EqualError(t, err, want)
	}
}

// TestValidateUnknownBackend checks that a build block of a backend no
// module of tt provides is a warning, whose fields are not checked, but in a
// hook, which runs make or shell only.
func TestValidateUnknownBackend(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, "[components.a]\npath = \"a\"\n[components.a.build]\n"+
		"backend = \"zig\"\nsteps = [\"install\"]\nanything = true\n")

	warnings, err := mfst.Validate(backends())
	require.NoError(t, err)
	assert.Equal(t, []string{`components.a.build: no module of this tt provides backend ` +
		`"zig"; its fields are not checked`}, warnings)

	_, err = parseBody(t, "[hooks.pre_build]\nbackend = \"zig\"\n").Validate(backends())
	require.EqualError(t, err,
		`hooks.pre_build: backend "zig" is not supported (want make or shell)`)

	_, err = parseBody(t, "[components.a]\npath = \"a\"\n[components.a.build]\n").
		Validate(backends())
	require.EqualError(t, err, "components.a.build: backend is required")
}

// TestValidateStructureOnly checks that Validate with no checker checks the
// structure of the build blocks and none of their fields.
func TestValidateStructureOnly(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, "[components.a]\npath = \"a\"\n[components.a.build]\n"+
		"backend = \"make\"\ncflags = [\"-O2\"]\njobs = 4\n"+
		"[hooks.pre_build]\ncommand = \"./gen.sh\"\ncgo = true\n")

	warnings, err := mfst.Validate(nil)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	_, err = parseBody(t, "[components.a]\npath = \"a\"\n[components.a.build]\n").Validate(nil)
	require.EqualError(t, err, "components.a.build: backend is required")

	// Which backends run hooks is the checker's to say.
	hook := parseBody(t, "[hooks.pre_build]\nbackend = \"exe\"\n")

	_, err = hook.Validate(nil)
	require.NoError(t, err)

	_, err = hook.Validate(backends())
	require.EqualError(t, err,
		`hooks.pre_build: backend "exe" is not supported (want make or shell)`)

	_, err = parseBody(t, "[hooks.pre_build]\ncommand = \"x\"\nstatic = true\n").Validate(nil)
	require.ErrorContains(t, err, "hooks.pre_build.static: is for the build of a component")
}

// TestBuildCanonical checks that the canonical form of a build block is that
// of what it says, its fields and those of its backend alike.
func TestBuildCanonical(t *testing.T) {
	t.Parallel()

	canonical := func(body string) []byte {
		t.Helper()

		form, err := parseBody(t, body).Components["a"].Build.Canonical()
		require.NoError(t, err)

		return form
	}

	block := "[components.a]\npath = \"a\"\n[components.a.build]\n"
	first := canonical(block + "backend = \"make\"\nmake_target = \"all\"\nenv = { A = \"1\" }\n")

	assert.Equal(t, first, canonical(block+"env.A = '1'\nmake_target = 'all'\nbackend = 'make'\n"),
		"the spelling of the block is not in its form")
	assert.NotEqual(t, first, canonical(block+"backend = \"make\"\nmake_target = \"x\"\n"+
		"env = { A = \"1\" }\n"))
	assert.NotEqual(t, first, canonical(block+"backend = \"make\"\nmake_target = \"all\"\n"+
		"env = { A = \"2\" }\n"))
}
