package backup

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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

// fakeClock is a wall clock that moves only when slept on. Each sleep advances
// it by step(d), so a test can model a sleep that ends early.
type fakeClock struct {
	t      time.Time
	step   func(d time.Duration) time.Duration
	sleeps []time.Duration
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) sleep(d time.Duration) {
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(c.step(d))
}

func TestNewBackupID_waitsOutItsSecond(t *testing.T) {
	clock := &fakeClock{
		t:    time.Date(2026, 9, 18, 14, 30, 0, 400*int(time.Millisecond), time.UTC),
		step: func(d time.Duration) time.Duration { return d },
	}

	id := newBackupID(clock.now, clock.sleep)

	require.Equal(t, "20260918T143000Z", id)
	require.Equal(t, []time.Duration{600 * time.Millisecond}, clock.sleeps)
	require.Equal(t, "20260918T143001Z", newBackupID(clock.now, clock.sleep),
		"the next call must land in the next second")
}

// TestNewBackupID_outlastsAShortSleep covers a sleep that returns before the
// wall clock has left the second: returning then would let the next call
// print the same id.
func TestNewBackupID_outlastsAShortSleep(t *testing.T) {
	short := true
	clock := &fakeClock{
		t: time.Date(2026, 9, 18, 14, 30, 0, 0, time.UTC),
		step: func(d time.Duration) time.Duration {
			if short {
				short = false
				return d / 2
			}
			return d
		},
	}

	id := newBackupID(clock.now, clock.sleep)

	require.Equal(t, "20260918T143000Z", id)
	require.Equal(t, time.Date(2026, 9, 18, 14, 30, 1, 0, time.UTC), clock.t,
		"must not return before the wall clock leaves the second")
	require.Equal(t, []time.Duration{time.Second, time.Second / 2}, clock.sleeps)
}

func TestNewBackupID_isUTC(t *testing.T) {
	zone := time.FixedZone("UTC+3", 3*60*60)
	clock := &fakeClock{
		t:    time.Date(2026, 9, 18, 17, 30, 0, 999*int(time.Millisecond), zone),
		step: func(d time.Duration) time.Duration { return d },
	}

	require.Equal(t, "20260918T143000Z", newBackupID(clock.now, clock.sleep))
}

// TestNewBackupID_consecutiveCallsSortUp runs the real clock: two ids taken
// one after the other differ, sort in the order they were taken, and are
// accepted as backup ids as they are.
func TestNewBackupID_consecutiveCallsSortUp(t *testing.T) {
	t.Parallel()

	first := NewBackupID()
	second := NewBackupID()

	require.Less(t, first, second)
	for _, id := range []string{first, second} {
		require.Regexp(t, `^\d{8}T\d{6}Z$`, id)
		require.NoError(t, ValidateBackupID(id))
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
