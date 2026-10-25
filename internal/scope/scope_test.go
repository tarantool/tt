package scope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/scope"
)

// fakeUname replaces PATH with a directory whose uname reports osName, or
// with an empty directory when osName is empty.
func fakeUname(t *testing.T, osName string) {
	t.Helper()

	toolDir := t.TempDir()
	if osName != "" {
		unamePath := filepath.Join(toolDir, "uname")
		script := "#!/bin/sh\nprintf '%s\\n' '" + osName + "'\n"
		require.NoError(t, os.WriteFile(unamePath, []byte(script), 0o700))
	}

	t.Setenv("PATH", toolDir)
}

func TestUserRoot(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "user home")
	dataHome := filepath.Join(t.TempDir(), "user data")
	userRoot := filepath.Join(t.TempDir(), "user root")
	cases := []struct {
		name     string
		osName   string
		homeDir  string
		dataHome string
		userRoot string
		want     string
		wantErr  string
	}{
		{
			name: "linux default", osName: "Linux", homeDir: homeDir,
			want: filepath.Join(homeDir, ".local", "share", "tarantool"),
		},
		{
			name: "linux XDG without home", osName: "Linux", dataHome: dataHome,
			want: filepath.Join(dataHome, "tarantool"),
		},
		{
			name: "linux relative XDG", osName: "Linux", homeDir: homeDir,
			dataHome: "relative/data", want: filepath.Join(homeDir, ".local", "share", "tarantool"),
		},
		{
			name: "macOS ignores XDG", osName: "Darwin", homeDir: homeDir, dataHome: dataHome,
			want: filepath.Join(homeDir, "Library", "Application Support", "tarantool"),
		},
		{
			name: "override over default", osName: "Linux", homeDir: homeDir,
			userRoot: userRoot, want: userRoot,
		},
		{
			name: "override over XDG", osName: "Linux", homeDir: homeDir, dataHome: dataHome,
			userRoot: userRoot, want: userRoot,
		},
		{
			name: "override on macOS", osName: "Darwin", homeDir: homeDir,
			userRoot: userRoot, want: userRoot,
		},
		{
			// The variable names the root outright: neither the OS nor the
			// home directory is needed.
			name: "override without uname and home", userRoot: userRoot, want: userRoot,
		},
		{
			name: "override on unknown OS", osName: "FreeBSD", userRoot: userRoot,
			want: userRoot,
		},
		{
			name: "override is cleaned", osName: "Linux", userRoot: userRoot + "/./sub/../",
			want: userRoot,
		},
		{
			name: "empty override", osName: "Linux", homeDir: homeDir, dataHome: dataHome,
			want: filepath.Join(dataHome, "tarantool"),
		},
		{
			name: "relative override", osName: "Linux", homeDir: homeDir, dataHome: dataHome,
			userRoot: "relative/root",
			wantErr:  `TT_USER_ROOT must be an absolute path, got "relative/root"`,
		},
		{
			name: "home-relative override", osName: "Linux", homeDir: homeDir,
			userRoot: "~/tarantool",
			wantErr:  `TT_USER_ROOT must be an absolute path, got "~/tarantool"`,
		},
		{
			name: "missing home", osName: "Linux",
			wantErr: "cannot resolve home directory for artifact path",
		},
		{
			name: "unknown OS", osName: "FreeBSD", homeDir: homeDir,
			wantErr: "artifact paths are unsupported for this operating system",
		},
		{
			name: "missing uname", homeDir: homeDir,
			wantErr: "cannot determine OS for artifact path",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fakeUname(t, testCase.osName)
			t.Setenv("HOME", testCase.homeDir)
			t.Setenv("XDG_DATA_HOME", testCase.dataHome)
			t.Setenv("TT_USER_ROOT", testCase.userRoot)
			// The other scope's variable must not leak into this one.
			t.Setenv("TT_SYSTEM_ROOT", filepath.Join(t.TempDir(), "system root"))

			root, err := scope.UserRoot()
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				assert.Empty(t, root)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.want, root)
				assert.True(t, filepath.IsAbs(root))
			}
		})
	}
}

func TestSystemRoot(t *testing.T) {
	systemRoot := filepath.Join(t.TempDir(), "system root")
	cases := []struct {
		name       string
		osName     string
		systemRoot string
		want       string
		wantErr    string
	}{
		{name: "linux default", osName: "Linux", want: "/opt/tarantool"},
		{
			name: "macOS default", osName: "Darwin",
			want: "/Library/Application Support/tarantool",
		},
		{
			name: "override over linux default", osName: "Linux", systemRoot: systemRoot,
			want: systemRoot,
		},
		{
			name: "override over macOS default", osName: "Darwin", systemRoot: systemRoot,
			want: systemRoot,
		},
		{name: "override without uname", systemRoot: systemRoot, want: systemRoot},
		{
			name: "override is cleaned", osName: "Linux", systemRoot: systemRoot + "//",
			want: systemRoot,
		},
		{name: "empty override", osName: "Linux", want: "/opt/tarantool"},
		{
			name: "relative override", osName: "Linux", systemRoot: "app/tarantool",
			wantErr: `TT_SYSTEM_ROOT must be an absolute path, got "app/tarantool"`,
		},
		{
			name: "unknown OS", osName: "FreeBSD",
			wantErr: "artifact paths are unsupported for this operating system",
		},
		{name: "missing uname", wantErr: "cannot determine OS for artifact path"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fakeUname(t, testCase.osName)
			t.Setenv("TT_SYSTEM_ROOT", testCase.systemRoot)
			// The other scope's variable must not leak into this one.
			t.Setenv("TT_USER_ROOT", filepath.Join(t.TempDir(), "user root"))

			root, err := scope.SystemRoot()
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				assert.Empty(t, root)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.want, root)
				assert.True(t, filepath.IsAbs(root))
			}
		})
	}
}

func TestRelativeRootIsErrRelativeRoot(t *testing.T) {
	for name, resolve := range map[string]func() (string, error){
		scope.UserRootEnv:   scope.UserRoot,
		scope.SystemRootEnv: scope.SystemRoot,
	} {
		t.Run(name, func(t *testing.T) {
			fakeUname(t, "Linux")
			t.Setenv("HOME", t.TempDir())
			t.Setenv(name, "relative")

			_, err := resolve()
			require.ErrorIs(t, err, scope.ErrRelativeRoot)
		})
	}
}
