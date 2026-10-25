package binary_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestScopeArtifactPathProject(t *testing.T) {
	parentDir := t.TempDir()
	currentDir := filepath.Join(parentDir, "app")
	require.NoError(t, os.Mkdir(filepath.Join(parentDir, ".tarantool"), 0o755))
	require.NoError(t, os.Mkdir(currentDir, 0o755))
	t.Chdir(currentDir)
	t.Setenv("PATH", "")
	t.Setenv("HOME", "")

	artifactPath, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(currentDir, ".tarantool"), artifactPath)
	assert.NoDirExists(t, artifactPath)
}

func TestScopeArtifactPath(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "user home")
	dataHome := filepath.Join(t.TempDir(), "user data")
	cases := []struct {
		name     string
		scope    binary.Scope
		osName   string
		homeDir  string
		dataHome string
		want     string
		wantErr  string
	}{
		{
			name: "linux XDG without home", scope: binary.ScopeUser, osName: "Linux",
			dataHome: dataHome, want: filepath.Join(dataHome, "tarantool"),
		},
		{
			name: "linux default", scope: binary.ScopeUser, osName: "Linux", homeDir: homeDir,
			want: filepath.Join(homeDir, ".local", "share", "tarantool"),
		},
		{
			name: "linux relative XDG", scope: binary.ScopeUser, osName: "Linux", homeDir: homeDir,
			dataHome: "relative/data", want: filepath.Join(homeDir, ".local", "share", "tarantool"),
		},
		{
			name: "macOS ignores XDG", scope: binary.ScopeUser, osName: "Darwin", homeDir: homeDir,
			dataHome: dataHome,
			want:     filepath.Join(homeDir, "Library", "Application Support", "tarantool"),
		},
		{
			name: "linux system", scope: binary.ScopeSystem, osName: "Linux",
			want: "/opt/tarantool",
		},
		{
			name: "macOS system", scope: binary.ScopeSystem, osName: "Darwin",
			want: "/Library/Application Support/tarantool",
		},
		{
			name: "missing home", scope: binary.ScopeUser, osName: "Linux",
			wantErr: "cannot resolve home directory for artifact path",
		},
		{
			name: "unknown OS", scope: binary.ScopeUser, osName: "FreeBSD",
			wantErr: "artifact paths are unsupported for this operating system",
		},
		{
			name: "missing uname", scope: binary.ScopeSystem,
			wantErr: "cannot determine OS for artifact path",
		},
		{
			name: "unknown scope", scope: binary.ScopeUnknown,
			wantErr: "unknown scope",
		},
		{
			name: "invalid scope", scope: binary.Scope(99),
			wantErr: "unknown scope",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			toolDir := t.TempDir()
			if testCase.osName != "" {
				unamePath := filepath.Join(toolDir, "uname")
				script := "#!/bin/sh\nprintf '%s\\n' '" + testCase.osName + "'\n"
				require.NoError(t, os.WriteFile(unamePath, []byte(script), 0o600))
				require.NoError(t, os.Chmod(unamePath, 0o700))
			}

			t.Setenv("PATH", toolDir)
			t.Setenv("HOME", testCase.homeDir)
			t.Setenv("XDG_DATA_HOME", testCase.dataHome)
			// The defaults are checked here; the overrides in internal/scope.
			t.Setenv("TT_USER_ROOT", "")
			t.Setenv("TT_SYSTEM_ROOT", "")

			artifactPath, err := testCase.scope.ArtifactPath()
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				assert.Empty(t, artifactPath)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.want, artifactPath)
				assert.True(t, filepath.IsAbs(artifactPath))
			}
		})
	}
}
