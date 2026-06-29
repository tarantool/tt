package version_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	goversion "github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/version"
)

// lockFile is the manifest lock; a change to it alone must not mark the tree
// dirty.
const lockFile = "app.manifest.lock"

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	//nolint:gosec // Fixed program (git) with test-controlled args.
	cmd := exec.CommandContext(
		context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

// initRepo creates a fresh repository with a deterministic identity.
func initRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")

	return dir
}

// commit records an empty commit so history can grow without touching files.
func commit(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "commit", "--allow-empty", "-q", "-m", msg)
}

func TestDerive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantMatch string
		wantDirty bool
	}{
		{
			name: "clean release tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.4.0")
			},
			wantMatch: `^1\.4\.0$`,
			wantDirty: false,
		},
		{
			name: "commits after release tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.4.0")
				commit(t, dir, "next")
			},
			wantMatch: `^1\.4\.1-dev\.1\+g[0-9a-f]+$`,
			wantDirty: false,
		},
		{
			name: "clean pre-release tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.4.0-rc.1")
			},
			wantMatch: `^1\.4\.0-rc\.1$`,
			wantDirty: false,
		},
		{
			name: "commits after pre-release tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.4.0-rc.1")
				commit(t, dir, "a")
				commit(t, dir, "b")
			},
			wantMatch: `^1\.4\.0-rc\.1\.dev\.2\+g[0-9a-f]+$`,
			wantDirty: false,
		},
		{
			name: "no tags",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "a")
				commit(t, dir, "b")
				commit(t, dir, "c")
			},
			wantMatch: `^0\.0\.0-dev\.3\+g[0-9a-f]+$`,
			wantDirty: false,
		},
		{
			name: "non-semver tag is skipped",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "a")
				git(t, dir, "tag", "release-2026")
				commit(t, dir, "b")
			},
			wantMatch: `^0\.0\.0-dev\.2\+g[0-9a-f]+$`,
			wantDirty: false,
		},
		{
			name: "uncommitted changes mark dirty",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.4.0")
				commit(t, dir, "next")
				require.NoError(t, os.WriteFile(
					filepath.Join(dir, "stray.txt"), []byte("x"), 0o600))
			},
			wantMatch: `^1\.4\.1-dev\.1\+g[0-9a-f]+\.dirty$`,
			wantDirty: true,
		},
		{
			name: "lock change alone is not dirty",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.4.0")
				require.NoError(t, os.WriteFile(
					filepath.Join(dir, lockFile), []byte("x"), 0o600))
			},
			wantMatch: `^1\.4\.0$`,
			wantDirty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := initRepo(t)
			tt.setup(t, dir)

			ver, err := version.Derive(dir)
			require.NoError(t, err)
			assert.Regexp(t, tt.wantMatch, ver.SemVer)
			assert.Equal(t, tt.wantDirty, ver.Dirty)

			// Whatever we render must round-trip as SemVer 2.0.0.
			_, err = goversion.NewSemver(ver.SemVer)
			assert.NoError(t, err, "rendered %q is not valid SemVer", ver.SemVer)
		})
	}
}

func TestDeriveVersionFileOverridesGit(t *testing.T) {
	t.Parallel()

	dir := initRepo(t)
	commit(t, dir, "init")
	git(t, dir, "tag", "v1.4.0")
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "VERSION"), []byte("v2.0.0\n"), 0o600))

	ver, err := version.Derive(dir)
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", ver.SemVer)
	assert.Empty(t, ver.Commit)
	assert.False(t, ver.Dirty)
}

func TestDeriveVersionFileRejectsNonSemVer(t *testing.T) {
	t.Parallel()

	dir := initRepo(t)
	commit(t, dir, "init")
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "VERSION"), []byte("final\n"), 0o600))

	_, err := version.Derive(dir)
	assert.Error(t, err)
}

func TestDeriveCommitShort(t *testing.T) {
	t.Parallel()

	dir := initRepo(t)
	commit(t, dir, "init")
	git(t, dir, "tag", "v1.4.0")
	commit(t, dir, "next")

	ver, err := version.Derive(dir)
	require.NoError(t, err)
	assert.NotEmpty(t, ver.Commit)
	assert.NotContains(t, ver.Commit, "g", "Commit must not keep the describe g prefix")
	assert.Regexp(t, `^[0-9a-f]+$`, ver.Commit)
}

// TestPreReleaseOrdering pins the lexical ordering the resolver relies on:
// a pre-release sorts below its release.
func TestPreReleaseOrdering(t *testing.T) {
	t.Parallel()

	prerelease, err := goversion.NewVersion("1.4.0-rc.1")
	require.NoError(t, err)

	release, err := goversion.NewVersion("1.4.0")
	require.NoError(t, err)

	assert.True(t, prerelease.LessThan(release), "1.4.0-rc.1 must sort below 1.4.0")
}
