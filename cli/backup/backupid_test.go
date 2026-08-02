package backup

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// unsafeBackupIDs are the ids that must never reach the filesystem, the storage
// or the chain ordering. Shared by the validator, start and finalize tests so
// the three entry points stay pinned to one table.
var unsafeBackupIDs = []struct {
	name string
	id   string
}{
	{"empty", ""},
	{"dot", "."},
	{"dot dot", ".."},
	{"leading dot", ".hidden"},
	{"parent escape", "../escape"},
	{"root escape", "../../sentinel"},
	{"inner traversal", "a/../../b"},
	{"nested date", "2026/08/02-full"},
	{"trailing separator", "id/"},
	{"absolute", "/abs"},
	{"backslash", `back\slash`},
	{"newline", "a\nb"},
	{"tab", "a\tb"},
	{"nul", "a\x00b"},
}

func TestValidateBackupID_rejectsUnsafeIDs(t *testing.T) {
	for _, tc := range unsafeBackupIDs {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBackupID(tc.id)
			require.ErrorIs(t, err, ErrInvalidBackupID)
			require.ErrorContains(t, err, strconv.Quote(tc.id),
				"the error must name the id it rejected")
		})
	}
}

// TestValidateBackupID_acceptsOrchestratorIDs pins the shapes real callers emit:
// timestamps, dashed and suffixed ids, and non-ASCII names. Rejecting any of
// them would break a working setup for no safety gain.
func TestValidateBackupID_acceptsOrchestratorIDs(t *testing.T) {
	ids := []string{
		"20260326T120000Z",
		"2026-01-01-full",
		"20260326T120000Z-inc1",
		"itest_full.1",
		"nightly.2026-01-01",
		"бэкап 01",
	}

	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			require.NoError(t, ValidateBackupID(id))
		})
	}
}

// dirNames returns the entry names of dir, sorted by os.ReadDir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// backupIDSandbox builds <base>/root/{tmp,sentinel} and points TMPDIR at
// root/tmp. Every traversal in unsafeBackupIDs then resolves inside base, so a
// regression shows up as a stray entry in the sandbox instead of a write to the
// machine's temp directory. sentinel is an empty directory: it is what finalize
// rmdirs when the id escapes the backup root.
func backupIDSandbox(t *testing.T) (base, root, tmpDir string) {
	t.Helper()

	base = t.TempDir()
	root = filepath.Join(base, "root")
	tmpDir = filepath.Join(root, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "sentinel"), 0o755))
	t.Setenv("TMPDIR", tmpDir)

	return base, root, tmpDir
}

// requireSandboxIntact checks that nothing was created under the backup root
// and nothing was created or removed above it.
func requireSandboxIntact(t *testing.T, base, root, tmpDir string) {
	t.Helper()

	require.Equal(t, []string{"root"}, dirNames(t, base))
	require.Equal(t, []string{"sentinel", "tmp"}, dirNames(t, root))
	require.Empty(t, dirNames(t, tmpDir))
}
