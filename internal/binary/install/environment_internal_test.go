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

func TestProjectEnvironmentOnlyProjectScope(t *testing.T) {
	for _, scope := range []binary.Scope{binary.ScopeUser, binary.ScopeSystem} {
		t.Run(scope.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, prepareProjectEnvironment(scope))
			require.NoDirExists(t, ".env")
		})
	}
}
