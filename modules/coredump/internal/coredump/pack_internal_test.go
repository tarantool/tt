package coredump

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // Changes the working directory.
func TestPackArgs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// The working directory as the process sees it: t.TempDir may be under
	// a symbolic link.
	cwd, err := filepath.Abs(".")
	require.NoError(t, err)

	args, err := packArgs(PackOptions{
		Core: "core", Executable: "bin/tarantool", Dir: "../out", PID: 42, Time: "1763869440",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"-c", filepath.Join(cwd, "core"),
		"-e", filepath.Join(cwd, "bin", "tarantool"),
		"-d", filepath.Join(filepath.Dir(cwd), "out"),
		"-p", "42",
		"-t", "1763869440",
	}, args)

	args, err = packArgs(PackOptions{Core: "/var/lib/systemd/coredump/core", Executable: "",
		Dir: "", PID: 0, Time: ""})
	require.NoError(t, err)
	assert.Equal(t, []string{"-c", "/var/lib/systemd/coredump/core"}, args)
}
