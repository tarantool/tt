package binary

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinkTarget(t *testing.T) {
	for _, testCase := range []struct {
		name, path, target, want string
	}{
		{"version", "/p/.tarantool/bin/tarantool",
			"/p/.tarantool/versions/tarantool/tarantool_3.9.0/tarantool",
			"../versions/tarantool/tarantool_3.9.0/tarantool"},
		{"headers", "/p/.tarantool/include/tarantool",
			"/p/.tarantool/versions/tarantool/tarantool_3.9.0/include/tarantool",
			"../versions/tarantool/tarantool_3.9.0/include/tarantool"},
		{"outside the scope", "/p/.tarantool/bin/tarantool", "/src/build/tarantool",
			"/src/build/tarantool"},
		{"a sibling named like the scope", "/p/.tarantool/bin/tt", "/p/.tarantool-old/tt",
			"/p/.tarantool-old/tt"},
		{"relative already", "/p/.tarantool/bin/tt", "../versions/tt/tt_3.0.0/tt",
			"../versions/tt/tt_3.0.0/tt"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, linkTarget(testCase.path, testCase.target))
		})
	}
}

// TestActivateSelectionsSurvivesMove: the active links of a scope resolve
// after the scope is moved.
func TestActivateSelectionsSurvivesMove(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "a", ".tarantool")
	installation := InstallationPath(root, ProgramCe, "3.9.0")
	require.NoError(t, os.MkdirAll(filepath.Join(installation, IncDirName, "tarantool"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(installation, "tarantool"), nil, 0o755))

	require.NoError(t, ActivateSelections(root, []Selection{{Program: ProgramCe, ID: "3.9.0"}}))
	require.NoError(t, os.Rename(filepath.Join(parent, "a"), filepath.Join(parent, "b")))

	moved := filepath.Join(parent, "b", ".tarantool")
	movedInstallation := InstallationPath(moved, ProgramCe, "3.9.0")

	for link, want := range map[string]string{
		filepath.Join(moved, BinDirName, "tarantool"): filepath.Join(movedInstallation,
			"tarantool"),
		filepath.Join(moved, IncDirName, "tarantool"): filepath.Join(movedInstallation,
			IncDirName, "tarantool"),
	} {
		resolved, err := filepath.EvalSymlinks(link)
		require.NoError(t, err)

		expected, err := filepath.EvalSymlinks(want)
		require.NoError(t, err)
		assert.Equal(t, expected, resolved)
	}

	buildPath, err := TarantoolBuildPath(filepath.Join(moved, BinDirName))
	require.NoError(t, err)
	assert.Empty(t, buildPath, "a moved scope has no local build")
}
