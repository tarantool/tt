package cmdcontext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/version"
)

func TestTarantoolCli_GetVersion(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "tnt.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool 2.11.0"`),
		0755)
	require.NoError(t, err)

	expectedVersion, err := version.Parse("2.11.0")
	require.NoError(t, err)

	tntCli := TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
	tntVersion, err := tntCli.GetVersion()
	require.NoError(t, err)
	require.Equal(t, expectedVersion, tntVersion)

	// Update "tarantool" executable and make sure cached version is still returned.
	err = os.WriteFile(filepath.Join(tmpDir, "tnt.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool 3.0.0"`),
		0755)
	require.NoError(t, err)

	tntVersion, err = tntCli.GetVersion()
	require.NoError(t, err)
	require.Equal(t, expectedVersion, tntVersion)

	// Check non-chached.
	tntCli = TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
	tntVersion, err = tntCli.GetVersion()
	require.NoError(t, err)
	require.Equal(t, version.Version{
		Major:   3,
		Minor:   0,
		Patch:   0,
		Release: version.Release{Type: version.TypeRelease},
		Str:     "3.0.0",
	}, tntVersion)
}

func TestTarantoolCli_GetVersionErrCases(t *testing.T) {
	tmpDir := t.TempDir()

	// Bad version format.
	err := os.WriteFile(filepath.Join(tmpDir, "tnt.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool version bad format"`),
		0755)
	require.NoError(t, err)

	tntCli := TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
	tntVersion, err := tntCli.GetVersion()
	assert.ErrorContains(t, err, "format is not valid")
	assert.Equal(t, version.Version{}, tntVersion)

	// Non-zero exit code.
	err = os.WriteFile(filepath.Join(tmpDir, "tnt.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool 2.11.0"
exit 1`),
		0755)
	require.NoError(t, err)

	tntCli = TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
	tntVersion, err = tntCli.GetVersion()
	assert.ErrorContains(t, err, "failed to get tarantool version: exit status 1")
	assert.Equal(t, version.Version{}, tntVersion)
}
