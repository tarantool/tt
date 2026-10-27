package running

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
)

// executableManifest is the manifest of the fixtures with an executable
// component that builds the program gw.
const executableManifest = fixtureManifest + "\n[components.gw]\npath = \"gw\"\n" +
	"kind = \"executable\"\n\n[components.gw.build]\nbackend = \"go\"\n"

// pathWithProgram puts a program named name into a fresh directory, makes
// that directory the only one of PATH, and returns the program's path.
func pathWithProgram(t *testing.T, name string) string {
	t.Helper()

	dir := t.TempDir()
	program := filepath.Join(dir, name)
	writeExecutable(t, program)
	t.Setenv("PATH", dir)

	return program
}

// TestResolveBinaryRocksBinFirst: a binary named without a slash is the
// program the package built into .rocks/bin of the application directory,
// in every scope, before a program of PATH that has its name; without one
// there, it is the program of PATH.
func TestResolveBinaryRocksBinFirst(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			hostProgram := pathWithProgram(t, "gw")

			fixture := newAppFixture(t, appScope, externalFiles(`{binary: gw}`))
			inst := fixture.instanceCtxOf(t, externalInst)

			path, err := ResolveBinary(&inst)
			require.NoError(t, err)
			assert.Equal(t, hostProgram, path)

			own := filepath.Join(fixture.dir, ".rocks", "bin", "gw")
			writeExecutable(t, own)

			path, err = ResolveBinary(&inst)
			require.NoError(t, err)
			assert.Equal(t, own, path)

			spec, err := instanceSpec(&inst, specOptions{})
			require.NoError(t, err)
			assert.Equal(t, own, spec.Path)
		})
	}
}

// TestResolveBinaryRocksBinNotExecutable: a file of .rocks/bin that cannot
// be run is an error, not a reason to run the program of PATH of its name.
func TestResolveBinaryRocksBinNotExecutable(t *testing.T) {
	pathWithProgram(t, "gw")

	fixture := newAppFixture(t, app.ScopeProject, externalFiles(`{binary: gw}`))
	writeFiles(t, fixture.dir, map[string]string{".rocks/bin/gw": "#!/bin/sh\n"})

	inst := fixture.instanceCtxOf(t, externalInst)

	_, err := ResolveBinary(&inst)
	require.ErrorIs(t, err, errNoExecutable)
	assert.Contains(t, err.Error(), filepath.Join(fixture.dir, ".rocks", "bin", "gw"))

	canStart, err := IsAbleToStartInstances([]InstanceCtx{inst})
	assert.False(t, canStart)
	require.ErrorIs(t, err, errNoExecutable)
}

// TestResolveBinaryNotFound: a program found nowhere is reported with both
// places it was looked for, and, in a project whose manifest has an
// executable component that builds it, with tt package build to run: start
// builds nothing. An installed version is not built, and gets no such hint.
func TestResolveBinaryNotFound(t *testing.T) {
	tests := []struct {
		name     string
		scope    app.Scope
		manifest string
		binary   string
		hint     string
	}{
		{"project", app.ScopeProject, fixtureManifest, "gw", ""},
		{
			"project with the component", app.ScopeProject, executableManifest, "gw",
			`; the executable component "gw" of the project builds it: run tt package build`,
		},
		{"another program", app.ScopeProject, executableManifest, "other", ""},
		{"installed version", app.ScopeUser, executableManifest, "gw", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())

			files := externalFiles(`{binary: ` + tt.binary + `}`)

			files["app.manifest.toml"] = tt.manifest

			fixture := newAppFixture(t, tt.scope, files)
			inst := fixture.instanceCtxOf(t, externalInst)

			_, err := ResolveBinary(&inst)
			require.ErrorIs(t, err, errNoExecutable)
			require.ErrorIs(t, err, exec.ErrNotFound)

			want := `instance "worker-1" of "myapp": cannot run the binary: "` +
				tt.binary + `" is neither in ` + filepath.Join(fixture.dir, ".rocks", "bin") +
				" nor in PATH" + tt.hint
			assert.Equal(t, want, err.Error())

			canStart, err := IsAbleToStartInstances([]InstanceCtx{inst})
			assert.False(t, canStart)
			assert.Equal(t, want, err.Error())

			_, err = os.Stat(filepath.Join(fixture.root, "var"))
			assert.True(t, os.IsNotExist(err), "a failed check creates nothing")
		})
	}
}
