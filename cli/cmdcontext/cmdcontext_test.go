package cmdcontext_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/version"
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

	tntCli := cmdcontext.TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
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
	tntCli = cmdcontext.TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
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

	tntCli := cmdcontext.TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
	tntVersion, err := tntCli.GetVersion()
	require.ErrorContains(t, err, "format is not valid")
	assert.Equal(t, version.Version{}, tntVersion)

	// Non-zero exit code.
	err = os.WriteFile(filepath.Join(tmpDir, "tnt.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool 2.11.0"
exit 1`),
		0o755)
	require.NoError(t, err)

	tntCli = cmdcontext.TarantoolCli{Executable: filepath.Join(tmpDir, "tnt.sh")}
	tntVersion, err = tntCli.GetVersion()
	require.ErrorContains(t, err, "failed to get tarantool version: exit status 1")
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
			expectedErrMsg: `failed to parse version "2131f7cc1de\n":` +
				` format is not valid`,
		},
		{
			name:           "version does not match",
			versionToCheck: "2.1.3.1.f7cc1de",
			expectedVer:    version.Version{},
			isErr:          true,
			expectedErrMsg: `the version of "2.1.3.1" does not match` +
				` <major>.<minor>.<patch> format`,
		},
		{
			name:           "hash does not match",
			versionToCheck: "2.1.3.f7cc1de_",
			expectedVer:    version.Version{},
			isErr:          true,
			expectedErrMsg: `hash "f7cc1de_" has a wrong format`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := os.WriteFile(filepath.Join(tmpDir, "tt.sh"),
				[]byte(fmt.Sprintf(`#!/bin/bash
    echo "%s"`, testCase.versionToCheck)),
				0o755)
			require.NoError(t, err)

			ttVersion, err := cmdcontext.GetTtVersion(filepath.Join(tmpDir, "tt.sh"))
			if testCase.isErr {
				require.EqualError(t, err, testCase.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, testCase.expectedVer, ttVersion)
		})
	}
}

// TestTtVersionOutputSelf checks how the version of another tt is asked for:
// with --self, which keeps an older tt from running the tt of its bin_dir in
// its place, and again without it only when the tt knows no --self.
func TestTtVersionOutputSelf(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		script string
		output string
		err    string
		calls  string
	}{
		{
			// An older tt answers with --self, and would answer for another
			// tt without it.
			name: "older tt",
			script: `if [ "$1" = --self ]; then echo 2.3.1.f7cc1de; ` +
				`else echo 9.9.9.0000000; fi`,
			output: "2.3.1.f7cc1de\n",
			calls:  "--self version --commit\n",
		},
		{
			// A tt without the flag refuses it and is asked again.
			name: "tt without --self",
			script: `if [ "$1" = --self ]; then ` +
				`echo "   ⨯ Error: unknown flag: --self" >&2; exit 1; fi; ` +
				`echo 3.0.0.f7cc1de`,
			output: "3.0.0.f7cc1de\n",
			calls:  "--self version --commit\nversion --commit\n",
		},
		{
			// Any other failure is not a reason to ask without --self.
			name:   "failing tt",
			script: `echo "something else" >&2; exit 2`,
			err:    "--self version --commit: exit status 2",
			calls:  "--self version --commit\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			tt := filepath.Join(dir, "tt")

			require.NoError(t, os.WriteFile(tt, []byte("#!/bin/sh\n"+
				`echo "$*" >> '`+calls+"'\n"+testCase.script+"\n"), 0o755))

			output, err := cmdcontext.TtVersionOutput(t.Context(), tt)
			if testCase.err != "" {
				require.ErrorContains(t, err, testCase.err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.output, string(output))
			}

			made, err := os.ReadFile(calls)
			require.NoError(t, err)
			assert.Equal(t, testCase.calls, string(made))
		})
	}
}
