package analyzer

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/remote"
)

// testPrograms are the programs of a fake build: each holds its name.
func testPrograms() map[Program][]byte {
	programs := make(map[Program][]byte, len(Programs))
	for _, program := range Programs {
		programs[program] = []byte("#!/bin/sh\necho " + string(program) + "\n")
	}

	return programs
}

// testRelease is the release table entry the tests build rocks of.
func testRelease(t *testing.T) Release {
	t.Helper()

	release, err := LookupRelease(DefaultVersion)
	require.NoError(t, err)

	return release
}

func TestBuildRockLayout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	path, err := BuildRock(dir, testRelease(t), "linux-x86_64", testPrograms())
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "emmylua-0.30.0pre1-1.linux-x86_64.rock"), path)

	reader, err := zip.OpenReader(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = reader.Close() })

	names := make([]string, 0, len(reader.File))

	var spec string

	for _, file := range reader.File {
		names = append(names, file.Name)

		if file.Name == "emmylua-0.30.0pre1-1.rockspec" {
			opened, err := file.Open()
			require.NoError(t, err)

			data, err := io.ReadAll(opened)
			require.NoError(t, err)
			require.NoError(t, opened.Close())

			spec = string(data)
		}
	}

	assert.ElementsMatch(t, []string{
		"emmylua-0.30.0pre1-1.rockspec", "rock_manifest",
		"bin/emmylua_check", "bin/emmylua_doc_cli", "bin/emmylua_ls", "bin/luafmt",
	}, names)
	assert.Contains(t, spec, "wrap_bin_scripts = false")
	assert.Contains(t, spec, `md5 = "2e5867117dccf42f0b36e8a751b592a8"`)
	assert.Contains(t, spec,
		"archive/599c33862491c5ca0f325e0ef549ad34c510ca97.tar.gz")
}

func TestBuildRockReproducible(t *testing.T) {
	t.Parallel()

	release := testRelease(t)

	first, err := BuildRock(t.TempDir(), release, "linux-x86_64", testPrograms())
	require.NoError(t, err)

	second, err := BuildRock(t.TempDir(), release, "linux-x86_64", testPrograms())
	require.NoError(t, err)

	firstData, err := os.ReadFile(first)
	require.NoError(t, err)

	secondData, err := os.ReadFile(second)
	require.NoError(t, err)

	assert.Equal(t, firstData, secondData)
}

func TestBuildRockMissingProgram(t *testing.T) {
	t.Parallel()

	programs := testPrograms()
	delete(programs, LanguageServer)

	_, err := BuildRock(t.TempDir(), testRelease(t), "linux-x86_64", programs)
	require.ErrorIs(t, err, ErrBuild)
	assert.Contains(t, err.Error(), "emmylua_ls")
}

// TestBuildRockInstalls installs the rock from an indexed registry: the
// programs land in the bin/ of the tree, as they are and executable. A
// server offers a host the rocks of its arch only, so the rock is of the
// host's.
func TestBuildRockInstalls(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the rock supports linux and macosx")
	}

	dir := t.TempDir()

	_, err := BuildRock(dir, testRelease(t), remote.HostArch(), testPrograms())
	require.NoError(t, err)
	require.NoError(t, indexRegistry(t.Context(), dir))

	tree := t.TempDir()
	rocks, err := client.New(luarocks.Config{
		Tree: tree, WorkingDir: t.TempDir(), Servers: []string{dir},
		Tarantool: luarocks.TarantoolConfig{Version: "3.3.0"},
	})
	require.NoError(t, err)
	require.NoError(t, rocks.Install(t.Context(), RockName, client.InstallOpts{
		Version: "0.30.0pre1-1", Servers: nil, Deps: client.DepsNone,
	}))

	for _, program := range Programs {
		path := filepath.Join(tree, "bin", string(program))

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, testPrograms()[program], data)

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.NotZero(t, info.Mode().Perm()&0o100, "%s is not executable", program)
	}
}

func TestReadBuild(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for program, data := range testPrograms() {
		require.NoError(t, os.WriteFile(filepath.Join(dir, string(program)), data, 0o755))
	}

	programs, err := ReadBuild(dir)
	require.NoError(t, err)
	assert.Equal(t, testPrograms(), programs)

	require.NoError(t, os.Chmod(filepath.Join(dir, string(Format)), 0o644))

	_, err = ReadBuild(dir)
	require.ErrorIs(t, err, ErrBuild)
	assert.Contains(t, err.Error(), "luafmt is not an executable")

	require.NoError(t, os.Remove(filepath.Join(dir, string(Format))))

	_, err = ReadBuild(dir)
	require.ErrorIs(t, err, ErrBuild)
}
