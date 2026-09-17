package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withProjectDir sets the -C value for one test and restores it afterwards.
// The flag is package state, so the tests that touch it cannot run in
// parallel.
func withProjectDir(t *testing.T, dir string) {
	t.Helper()

	previous := projectDir
	projectDir = dir

	t.Cleanup(func() { projectDir = previous })
}

// TestAbsoluteWorkingDirDefault checks that without -C the working directory
// is what the project-scope commands get.
func TestAbsoluteWorkingDirDefault(t *testing.T) {
	withProjectDir(t, "")

	dir, err := absoluteWorkingDir()
	require.NoError(t, err)

	wd, err := os.Getwd()
	require.NoError(t, err)

	wd, err = filepath.Abs(wd)
	require.NoError(t, err)

	assert.Equal(t, wd, dir)
}

// TestAbsoluteWorkingDirFlag checks that -C replaces the working directory and
// is returned absolute, whether it was given absolute or relative.
func TestAbsoluteWorkingDirFlag(t *testing.T) {
	project := t.TempDir()

	t.Run("absolute", func(t *testing.T) {
		withProjectDir(t, project)

		dir, err := absoluteWorkingDir()
		require.NoError(t, err)

		// t.TempDir can hand back a path through a symlink (/var on macOS),
		// and absoluteWorkingDir does not resolve those; compare what it was
		// given, made absolute the same way.
		want, err := filepath.Abs(project)
		require.NoError(t, err)
		assert.Equal(t, want, dir)
	})

	t.Run("relative", func(t *testing.T) {
		nested := filepath.Join(project, "nested")
		require.NoError(t, os.Mkdir(nested, 0o755))

		wd, err := os.Getwd()
		require.NoError(t, err)

		require.NoError(t, os.Chdir(project))
		t.Cleanup(func() { require.NoError(t, os.Chdir(wd)) })

		withProjectDir(t, "nested")

		dir, err := absoluteWorkingDir()
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(dir), "must be absolute, got %q", dir)
		assert.Equal(t, "nested", filepath.Base(dir))
	})
}

// TestAbsoluteWorkingDirRejects checks that a -C path that is not a usable
// directory is reported as a -C problem, not left to whatever opens a file
// next.
func TestAbsoluteWorkingDirRejects(t *testing.T) {
	project := t.TempDir()

	t.Run("missing", func(t *testing.T) {
		withProjectDir(t, filepath.Join(project, "absent"))

		_, err := absoluteWorkingDir()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "-C ")
	})

	t.Run("file", func(t *testing.T) {
		file := filepath.Join(project, "app.manifest.toml")
		require.NoError(t, os.WriteFile(file, []byte("manifest_version = \"0.1\"\n"), 0o644))

		withProjectDir(t, file)

		_, err := absoluteWorkingDir()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})
}

// TestProjectDirFlagIsWired checks that the commands which act on a project
// accept -C. tt run is deliberately not among them: it parses no flags of its
// own, every argument being Tarantool's.
func TestProjectDirFlagIsWired(t *testing.T) {
	assert.NotNil(t, NewPackageCmd().PersistentFlags().Lookup("directory"),
		"tt package must pass -C down to every subcommand")
	assert.NotNil(t, NewNewCmd().Flags().Lookup("directory"))
	assert.NotNil(t, NewTestCmd().Flags().Lookup("directory"))
	assert.Nil(t, NewRunCmd().Flags().Lookup("directory"))
}
