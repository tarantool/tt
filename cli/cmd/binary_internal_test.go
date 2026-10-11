package cmd

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBinaryInstallSDK checks that SDK retains the download flags and reaches
// the downloader without requiring an environment config or installing a binary.
//
//nolint:paralleltest // Command constructors and handlers share process state.
func TestBinaryInstallSDK(t *testing.T) {
	restoreProcessState(t)

	previousInstall, previousDownload := installCtx, downloadCtx

	t.Cleanup(func() {
		installCtx, downloadCtx = previousInstall, previousDownload
	})

	root := NewCmdRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	cmdCtx.Cli.ConfigPath = ""

	destination := filepath.Join(t.TempDir(), "missing")
	root.SetArgs([]string{
		"binary", "install", "SDK", "3.0.0", "--dev", "--directory-prefix", destination,
	})

	command, err := root.ExecuteC()
	require.ErrorContains(t, err, "bad directory prefix:")
	assert.Equal(t, "tt binary install SDK", command.CommandPath())
	assert.Equal(t, "SDK", cmdCtx.CommandName)
	assert.Equal(t, "3.0.0", downloadCtx.Version)
	assert.Equal(t, destination, downloadCtx.DirectoryPrefix)
	assert.True(t, downloadCtx.DevBuild)
	assert.False(t, installCtx.DevBuild)
}

// TestBinaryArgumentValidation checks the argument contracts on the new paths
// without making network requests or modifying an installed program.
//
//nolint:paralleltest // Command constructors bind flags to process state.
func TestBinaryArgumentValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"install", "SDK"}, "you need to specify the version"},
		{[]string{"install", "SDK", "3.0", "3.1"}, "invalid number of parameters"},
		{[]string{"install", "tarantool", "3.0", "3.1"}, "accepts at most 1 arg(s)"},
		{[]string{"uninstall", "tarantool-dev", "3.0"}, "accepts 0 arg(s)"},
		{[]string{"search", "tarantool", "3.0"}, "accepts 0 arg(s)"},
		{[]string{"switch", "unknown"}, "not supported program: unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			command, args, err := NewBinaryCmd().Find(tc.args)
			require.NoError(t, err)
			require.NotNil(t, command.Args)
			assert.ErrorContains(t, command.ValidateArgs(args), tc.want)
		})
	}
}
