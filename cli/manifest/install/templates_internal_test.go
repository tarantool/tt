package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/exitcode"
)

// templateFiles are the archive entries of one complete template.
func templateFiles(name string) map[string]string {
	return map[string]string{
		".templates/" + name + "/instances.yaml": "router-1:\n",
		".templates/" + name + "/config.yaml":    "groups: {}\n",
	}
}

// withTemplatesSpec is a with-deps archive of my-app carrying a default
// template.
func withTemplatesSpec() archiveSpec {
	return archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "3.0.5",
		files: mergeFiles(rockFiles("my-app", "1.0.0"), templateFiles("default")),
	}
}

// TestReadHeaderReportsTemplates: the header tells whether the archive carries
// .templates/, independently of its packing mode.
func TestReadHeaderReportsTemplates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec archiveSpec
		want bool
	}{
		{"with deps and templates", withTemplatesSpec(), true},
		{"without deps and templates", archiveSpec{
			name: "my-app", version: "1.0.0", files: templateFiles("default"),
		}, true},
		{"no templates", archiveSpec{name: "my-app", version: "1.0.0"}, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ar, err := OpenArchive(test.spec.build(t))
			require.NoError(t, err)

			header, err := ar.ReadHeader()
			require.NoError(t, err)
			assert.Equal(t, test.want, header.HasTemplates)
		})
	}
}

// TestExtractMapperTemplates: .templates/ lands at the project install-root as
// is, and nowhere in the user and system trees.
func TestExtractMapperTemplates(t *testing.T) {
	t.Parallel()

	entry := ".templates/default/instances.yaml"

	dst, ok := extractMapper(ScopeProject, nil, false)(entry)
	assert.True(t, ok)
	assert.Equal(t, entry, dst)

	for _, scope := range []Scope{ScopeUser, ScopeSystem} {
		_, ok := extractMapper(scope, nil, false)(entry)
		assert.False(t, ok, scope)
	}
}

// TestInstallLaysTemplatesIntoProject: a project install puts the archive's
// templates into the install-root, which is where tt instance looks for them.
func TestInstallLaysTemplatesIntoProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := installInto(t, dir, Options{Scope: ScopeProject}, withTemplatesSpec().build(t))
	require.NoError(t, err)

	assert.Equal(t, "router-1:\n",
		readFile(t, filepath.Join(dir, ".templates", "default", "instances.yaml")))
	assert.Equal(t, "groups: {}\n",
		readFile(t, filepath.Join(dir, ".templates", "default", "config.yaml")))
}

// TestInstallRefusesExistingTemplates: an install-root that already has
// .templates/ is not laid over, and the refusal comes before anything is
// written.
func TestInstallRefusesExistingTemplates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mine := filepath.Join(dir, ".templates", "mine", "instances.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(mine), dirPerm))
	require.NoError(t, os.WriteFile(mine, []byte("mine-1:\n"), filePerm))

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, withTemplatesSpec().build(t))

	require.ErrorIs(t, err, errTemplatesExist)
	assert.Equal(t, 1, exitcode.Code(err))
	assert.Contains(t, err.Error(), "my-app")

	// Nothing was written: no package, no runtime, no template of the archive.
	assert.NoDirExists(t, filepath.Join(dir, ".rocks"))
	assert.NoDirExists(t, filepath.Join(dir, "_runtime"))
	assert.NoDirExists(t, filepath.Join(dir, ".templates", "default"))
	assert.Equal(t, "mine-1:\n", readFile(t, mine))
}

// TestInstallForceDoesNotReplaceTemplates pins the conservative side of the
// rule: install keeps no record of who laid .templates/, so even a --force
// reinstall of the package that did refuses to lay over it.
func TestInstallForceDoesNotReplaceTemplates(t *testing.T) {
	t.Parallel()

	archive := withTemplatesSpec().build(t)
	dir := t.TempDir()

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, archive)
	require.NoError(t, err)

	_, err = installInto(t, dir, Options{Scope: ScopeProject, Force: true}, archive)
	require.ErrorIs(t, err, errTemplatesExist)

	require.NoError(t, os.RemoveAll(filepath.Join(dir, ".templates")))

	_, err = installInto(t, dir, Options{Scope: ScopeProject, Force: true}, archive)
	require.NoError(t, err, "with .templates/ moved away the reinstall goes through")
	assert.FileExists(t, filepath.Join(dir, ".templates", "default", "instances.yaml"))
}

// TestInstallWithoutTemplatesKeepsExisting: an archive with no templates has
// nothing to lay over, so an existing .templates/ neither blocks it nor is
// touched.
func TestInstallWithoutTemplatesKeepsExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mine := filepath.Join(dir, ".templates", "mine", "instances.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(mine), dirPerm))
	require.NoError(t, os.WriteFile(mine, []byte("mine-1:\n"), filePerm))

	archive := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "3.0.5",
		files: rockFiles("my-app", "1.0.0"),
	}.build(t)

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, archive)
	require.NoError(t, err)
	assert.Equal(t, "mine-1:\n", readFile(t, mine))
	assert.NoDirExists(t, filepath.Join(dir, ".templates", "default"))
}

// TestInstallUserScopeLeavesTemplatesOut: the user tree is a LuaRocks tree with
// no application directory, so the templates stay in the archive - the
// behaviour of every install before .templates/ was packed. Not parallel: the
// user scope's root comes from HOME.
func TestInstallUserScopeLeavesTemplatesOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	archive := archiveSpec{
		name: "my-app", version: "1.0.0",
		files: mergeFiles(rockFiles("my-app", "1.0.0"), templateFiles("default")),
	}.build(t)

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser}, archive)
	require.NoError(t, err)

	root := filepath.Join(home, ".luarocks")
	assert.FileExists(t, filepath.Join(root, "share", "tarantool", "my-app", "init.lua"))
	assert.NoDirExists(t, filepath.Join(root, ".templates"))
	assert.NoDirExists(t, filepath.Join(home, ".templates"))
}
