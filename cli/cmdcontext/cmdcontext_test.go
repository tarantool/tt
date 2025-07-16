package cmdcontext

import (
	"fmt"
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
		0o755)
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
		0o755)
	require.NoError(t, err)

	tntVersion, err = tntCli.GetVersion()
	require.NoError(t, err)
	require.Equal(t, expectedVersion, tntVersion)

	// Check non-cached.
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
		0o755)
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
		0o755)
	require.NoError(t, err)

	tntCli = TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
	tntVersion, err = tntCli.GetVersion()
	assert.ErrorContains(t, err, "failed to get tarantool version: exit status 1")
	assert.Equal(t, version.Version{}, tntVersion)
}

func TestTtCli_GetVersion(t *testing.T) {
	tmpDir := t.TempDir()

	type testCase struct {
		name           string
		versionToCheck string
		expectedVer    version.Version
		isErr          bool
		expectedErrMsg string
	}

	cases := []testCase{
		{
			name:           "basic",
			versionToCheck: "2.3.1.f7cc1de\n",
			expectedVer: version.Version{
				Major: 2,
				Minor: 3,
				Patch: 1,
				Hash:  "f7cc1de",
				Str:   "2.3.1.f7cc1de",
			},
			isErr:          false,
			expectedErrMsg: "",
		},
		{
			name:           "parse error",
			versionToCheck: "2.w.1.f7cc1de",
			expectedVer:    version.Version{},
			isErr:          true,
			expectedErrMsg: `strconv.ParseUint: parsing "w": invalid syntax`,
		},
		{
			name:           "no dots in version",
			versionToCheck: "2131f7cc1de",
			expectedVer:    version.Version{},
			isErr:          true,
			expectedErrMsg: fmt.Sprintf(`failed to parse version "2131f7cc1de\n":` +
				` format is not valid`),
		},
		{
			name:           "version does not match",
			versionToCheck: "2.1.3.1.f7cc1de",
			expectedVer:    version.Version{},
			isErr:          true,
			expectedErrMsg: fmt.Sprintf(`the version of "2.1.3.1" does not match` +
				` <major>.<minor>.<patch> format`),
		},
		{
			name:           "hash does not match",
			versionToCheck: "2.1.3.f7cc1de_",
			expectedVer:    version.Version{},
			isErr:          true,
			expectedErrMsg: `hash "f7cc1de_" has a wrong format`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := os.WriteFile(filepath.Join(tmpDir, "tt.sh"),
				[]byte(fmt.Sprintf(`#!/bin/bash
    echo "%s"`, tc.versionToCheck)),
				0o755)
			require.NoError(t, err)

			ttVersion, err := GetTtVersion(filepath.Join(tmpDir, "tt.sh"))
			if tc.isErr {
				require.EqualError(t, err, tc.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.expectedVer, ttVersion)
		})
	}
}
