package restore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// An archive cut short mid-stream is a failure, not a rejected input: the
// caller supplied no checksum to catch it earlier, so the unpack path has to
// report it -- and must not leave a marker claiming the directory is ready.
func TestApply_TruncatedArchiveFails(t *testing.T) {
	full, _ := archiveChain(t)

	raw, err := os.ReadFile(full)
	require.NoError(t, err)

	trunc := filepath.Join(t.TempDir(), "truncated.tar.zst")
	require.NoError(t, os.WriteFile(trunc, raw[:len(raw)/2], 0o644))

	workDir := filepath.Join(t.TempDir(), "wd")

	_, err = Apply(ApplyOpts{Archives: []string{trunc}, WorkDir: workDir})
	require.Error(t, err)
	require.ErrorContains(t, err, trunc)
	require.False(t, errors.Is(err, ErrValidation),
		"a mid-stream failure is not a rejected input")

	_, err = ReadState(workDir)
	require.Error(t, err, "a failed run must not leave a marker")
}

// A work directory the process cannot write into fails on the first entry,
// with the unpack error naming what went wrong.
func TestApply_UnwritableWorkDirFailsOnUnpack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root is not stopped by directory permissions")
	}

	full, _ := archiveChain(t)

	workDir := filepath.Join(t.TempDir(), "wd")
	require.NoError(t, os.MkdirAll(workDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(workDir, 0o755) })

	_, err := Apply(ApplyOpts{Archives: []string{full}, WorkDir: workDir})
	require.ErrorContains(t, err, "failed to unpack")

	_, err = ReadState(workDir)
	require.Error(t, err, "a failed run must not leave a marker")
}

// A --work-dir naming a regular file can never be prepared, and the file is
// left exactly as it was -- but the run reports it as a failure rather than as
// a rejected input, so this input alone exits 1 where every other unusable one
// exits 3 ("nothing was touched"), which is precisely what happened here.
// Flip the NotErrorIs once the check moves into validate.
func TestApply_WorkDirIsAFileIsReportedWithoutTouchingIt(t *testing.T) {
	full, _ := archiveChain(t)

	workDir := filepath.Join(t.TempDir(), "instance-001")
	require.NoError(t, os.WriteFile(workDir, []byte("not a directory"), 0o644))

	_, err := Apply(ApplyOpts{Archives: []string{full}, WorkDir: workDir})
	require.ErrorContains(t, err, "failed to create work directory")
	require.False(t, errors.Is(err, ErrValidation),
		"today an unusable work directory is not reported as a rejected input")

	body, err := os.ReadFile(workDir)
	require.NoError(t, err)
	require.Equal(t, "not a directory", string(body))

	require.NoFileExists(t, StatePath(workDir))
}

// A marker mangled by a crash between runs must not stop the next run:
// prepare drops it before rebuilding, and a fresh valid marker replaces it.
func TestApply_CorruptMarkerIsReplacedOnRerun(t *testing.T) {
	full, inc := archiveChain(t)
	workDir := filepath.Join(t.TempDir(), "wd")

	opts := ApplyOpts{Archives: []string{full, inc}, WorkDir: workDir}

	_, err := Apply(opts)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(StatePath(workDir), []byte(`{"work_dir": tru`), 0o644))
	_, err = ReadState(workDir)
	require.ErrorContains(t, err, "failed to decode restore state")

	_, err = Apply(opts)
	require.NoError(t, err)

	state, err := ReadState(workDir)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(workDir), state.WorkDir)
}

func TestReadState_MissingMarker(t *testing.T) {
	_, err := ReadState(filepath.Join(t.TempDir(), "wd"))
	require.ErrorContains(t, err, "failed to read restore state")
}

// A marker path occupied by a non-empty directory cannot be cleared, and the
// run must stop right there: carrying on would end with a marker describing
// a directory it does not belong to.
func TestApply_UndeletableMarkerIsReported(t *testing.T) {
	full, _ := archiveChain(t)
	workDir := filepath.Join(t.TempDir(), "wd")

	marker := StatePath(workDir)
	require.NoError(t, os.MkdirAll(filepath.Join(marker, "occupied"), 0o755))

	_, err := Apply(ApplyOpts{Archives: []string{full}, WorkDir: workDir})
	require.ErrorContains(t, err, "failed to remove stale restore state")
}
