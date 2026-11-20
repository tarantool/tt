package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/v3/execfile"
	"github.com/tarantool/tt/v3/internal/cmakepolicy"
)

// fakeBuildTools puts a cmake and a make into PATH that write the CMake policy
// version of their environment, or "unset", into <dir>/cmake and <dir>/make.
func fakeBuildTools(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(bin, defaultDirPermissions))

	for _, tool := range []string{cmakeExec, makeExec} {
		script := "#!/bin/sh\nprintf '%s' \"${" + cmakepolicy.Variable + "-unset}\" > '" +
			filepath.Join(dir, tool) + "'\n"
		// The tests run the scripts while parallel tests fork: execfile
		// keeps the forks off the descriptor it writes them through.
		require.NoError(t, execfile.WriteFile(filepath.Join(bin, tool), []byte(script), 0o755))
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return dir
}

// TestBuildTarantoolCMakePolicy: the cmake and the make that build Tarantool
// from source get the policy version CMake 4 needs to configure an old commit,
// unless the user has set one, which they get as it is.
func TestBuildTarantoolCMakePolicy(t *testing.T) {
	cases := []struct {
		name string
		// user is the value the user sets, nil: none.
		user *string
		want string
	}{
		{name: "unset", user: nil, want: cmakepolicy.Version},
		{name: "user", user: new("3.10"), want: "3.10"},
		{name: "empty", user: new(""), want: ""},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := fakeBuildTools(t)

			t.Setenv(cmakepolicy.Variable, "")

			if test.user == nil {
				require.NoError(t, os.Unsetenv(cmakepolicy.Variable))
			} else {
				t.Setenv(cmakepolicy.Variable, *test.user)
			}

			logFile, err := os.Create(filepath.Join(dir, "log"))
			require.NoError(t, err)

			t.Cleanup(func() { assert.NoError(t, logFile.Close()) })

			for _, dynamic := range []bool{false, true} {
				_, err = buildTarantool(t.TempDir(), InstallCtx{Dynamic: dynamic}, logFile)
				require.NoError(t, err)

				for _, tool := range []string{cmakeExec, makeExec} {
					got, err := os.ReadFile(filepath.Join(dir, tool))
					require.NoError(t, err)
					assert.Equal(t, test.want, string(got), "%s, dynamic=%v", tool, dynamic)
				}
			}

			_, set := os.LookupEnv(cmakepolicy.Variable)
			assert.Equal(t, test.user != nil, set, "the environment of tt is not changed")
		})
	}
}
