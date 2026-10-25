package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestProjectEnvironmentPreservesScripts(t *testing.T) {
	for _, existing := range []string{"activate.sh", "deactivate.sh"} {
		t.Run(existing, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir(".env", 0o755))

			path := filepath.Join(".env", existing)
			original := []byte("# User-provided environment script.\n")
			require.NoError(t, os.WriteFile(path, original, 0o600))
			require.NoError(t, prepareProjectEnvironment(binary.ScopeProject))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, original, data)

			for _, name := range []string{"activate.sh", "deactivate.sh"} {
				path := filepath.Join(".env", name)
				data, err := os.ReadFile(path)
				require.NoError(t, err)

				data = append(data, []byte("# Keep custom changes.\n")...)
				require.NoError(t, os.WriteFile(path, data, 0o600))
				require.NoError(t, prepareProjectEnvironment(binary.ScopeProject))

				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, data, after)
			}
		})
	}
}

func TestProjectEnvironmentFromSubdirectory(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	currentDir := filepath.Join(project, "src", "lib")
	require.NoError(t, os.MkdirAll(currentDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "app.manifest.toml"), nil, 0o644))
	t.Chdir(currentDir)

	require.NoError(t, prepareProjectEnvironment(binary.ScopeProject))
	require.NoDirExists(t, filepath.Join(currentDir, ".env"))
	require.FileExists(t, filepath.Join(project, ".env", "deactivate.sh"))

	activate, err := os.ReadFile(filepath.Join(project, ".env", "activate.sh"))
	require.NoError(t, err)
	require.Contains(t, string(activate), "export TT_PROJECT_DIR='"+project+"'\n")
}

func TestProjectActivateCommand(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project with spaces and 'quotes'")
	require.NoError(t, os.MkdirAll(filepath.Join(project, "src", "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "app.manifest.toml"), nil, 0o644))

	for directory, want := range map[string]string{
		".":       "source .env/activate.sh",
		"src":     "source ../.env/activate.sh",
		"src/lib": "source ../../.env/activate.sh",
	} {
		t.Run(directory, func(t *testing.T) {
			t.Chdir(filepath.Join(project, directory))
			require.Equal(t, want, projectActivateCommand())
		})
	}

	t.Run("outside a project", func(t *testing.T) {
		t.Chdir(t.TempDir())
		require.Equal(t, "source .env/activate.sh", projectActivateCommand())
	})
}

func TestProjectEnvironmentOnlyProjectScope(t *testing.T) {
	for _, scope := range []binary.Scope{binary.ScopeUser, binary.ScopeSystem} {
		t.Run(scope.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, prepareProjectEnvironment(scope))
			require.NoDirExists(t, ".env")
		})
	}
}
