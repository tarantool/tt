package pack

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitRepo makes a temp dir into a git repo with one commit and returns it.
func gitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"init.lua": "return 1"})

	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")

	return dir
}

// git runs one git command in dir and fails the test on a non-zero exit.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// TestBuildTimeIsDeterministic is the reproducibility guarantee at its source.
// version.lua embeds built_at, so a wall-clock stamp makes the same commit pack
// to a different archive every time and the checksum meaningless.
func TestBuildTimeIsDeterministic(t *testing.T) {
	dir := gitRepo(t)

	first := buildTime(context.Background(), dir)
	time.Sleep(1100 * time.Millisecond)
	second := buildTime(context.Background(), dir)

	assert.Equal(t, first, second, "the same commit must yield the same timestamp")
	assert.False(t, first.IsZero())
}

// TestBuildTimeHonorsSourceDateEpoch lets CI pin the stamp explicitly.
func TestBuildTimeHonorsSourceDateEpoch(t *testing.T) {
	t.Setenv(sourceDateEpochEnv, "1700000000")

	assert.Equal(t, time.Unix(1700000000, 0).UTC(), buildTime(context.Background(), gitRepo(t)))
}

func TestBuildTimeIgnoresMalformedEpoch(t *testing.T) {
	t.Setenv(sourceDateEpochEnv, "not-a-number")

	dir := gitRepo(t)
	// Falls through to the commit timestamp rather than failing.
	assert.Equal(t, buildTime(context.Background(), dir), buildTime(context.Background(), dir))
}

// TestBuildTimeOutsideGitFallsBack covers a tree with no history, where
// reproducibility is unreachable and the wall clock is the honest answer.
func TestBuildTimeOutsideGitFallsBack(t *testing.T) {
	before := time.Now().Add(-time.Second)

	got := buildTime(context.Background(), t.TempDir())

	assert.False(t, got.Before(before), "expected a wall-clock stamp")
}
