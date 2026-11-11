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

	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// lockFile is the manifest lock; a change to it alone must not mark the tree
// dirty.
const lockFile = "app.manifest.lock"

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()

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
	git(t, dir, "config", "tag.gpgsign", "false")

	return dir
}

// commit records an empty commit so history can grow without touching files.
func commit(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "commit", "--allow-empty", "-q", "-m", msg)
}

// writeFile writes content to rel, a slash path under dir.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()

	path := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// commitAll commits every change of the repository at dir.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
}

func TestDerive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantMatch string
		wantDirty bool
		wantTag   string
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
			wantTag:   "v1.4.0",
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
			wantTag:   "",
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
			wantTag:   "v1.4.0-rc.1",
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
			wantTag:   "",
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
			wantTag:   "",
		},
		{
			name: "clean bare release tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "1.1.2")
			},
			wantMatch: `^1\.1\.2$`,
			wantDirty: false,
			wantTag:   "1.1.2",
		},
		{
			name: "commits after bare release tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "1.1.2")
				commit(t, dir, "next")
			},
			wantMatch: `^1\.1\.3-dev\.1\+g[0-9a-f]+$`,
			wantDirty: false,
			wantTag:   "",
		},
		{
			name: "clean bare pre-release tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "1.2.0-rc.1")
			},
			wantMatch: `^1\.2\.0-rc\.1$`,
			wantDirty: false,
			wantTag:   "1.2.0-rc.1",
		},
		{
			name: "a bare tag past a v tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.0.0")
				commit(t, dir, "next")
				git(t, dir, "tag", "1.1.0")
				commit(t, dir, "more")
			},
			wantMatch: `^1\.1\.1-dev\.1\+g[0-9a-f]+$`,
			wantDirty: false,
			wantTag:   "",
		},
		{
			// git describe names the first by refname of two lightweight tags.
			name: "the v tag of a version over the bare one",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "1.2.0")
				git(t, dir, "tag", "v1.2.0")
			},
			wantMatch: `^1\.2\.0$`,
			wantDirty: false,
			wantTag:   "v1.2.0",
		},
		{
			// git describe names an annotated tag over a lightweight one.
			name: "the v tag of a version over an annotated bare one",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.2.0")
				git(t, dir, "tag", "-a", "1.2.0", "-m", "1.2.0")
			},
			wantMatch: `^1\.2\.0$`,
			wantDirty: false,
			wantTag:   "v1.2.0",
		},
		{
			name: "the highest version of a commit",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.2.0")
				git(t, dir, "tag", "-a", "v1.2.0-rc.1", "-m", "rc")
				git(t, dir, "tag", "1.1.9")
			},
			wantMatch: `^1\.2\.0$`,
			wantDirty: false,
			wantTag:   "v1.2.0",
		},
		{
			name: "a dev version of the highest version of a commit",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "-a", "1.2.0-rc.1", "-m", "rc")
				git(t, dir, "tag", "v1.2.0")
				commit(t, dir, "next")
			},
			wantMatch: `^1\.2\.1-dev\.1\+g[0-9a-f]+$`,
			wantDirty: false,
			wantTag:   "",
		},
		{
			name: "tags that are no versions are passed over",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.0.0")
				commit(t, dir, "next")
				git(t, dir, "tag", "v1.x")
				git(t, dir, "tag", "2026-11-12")
				git(t, dir, "tag", "-a", "1.2", "-m", "1.2")
			},
			wantMatch: `^1\.0\.1-dev\.1\+g[0-9a-f]+$`,
			wantDirty: false,
			wantTag:   "",
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
			wantTag:   "",
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
			wantTag:   "",
		},
		{
			name: "a dirty tree on a tag is not the tag",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				commit(t, dir, "init")
				git(t, dir, "tag", "v1.4.0")
				require.NoError(t, os.WriteFile(
					filepath.Join(dir, "stray.txt"), []byte("x"), 0o600))
			},
			wantMatch: `^1\.4\.0\+dirty$`,
			wantDirty: true,
			wantTag:   "",
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
			wantTag:   "v1.4.0",
		},
		{
			// git status prints " M app.manifest.lock" for it: the status
			// starts with a blank.
			name: "a regenerated lock of a commit alone is not dirty",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeFile(t, dir, lockFile, "a\n")
				commitAll(t, dir, "init")
				git(t, dir, "tag", "v1.4.0")
				writeFile(t, dir, lockFile, "b\n")
			},
			wantMatch: `^1\.4\.0$`,
			wantDirty: false,
			wantTag:   "v1.4.0",
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
			assert.Equal(t, tt.wantTag, ver.Tag)

			// Whatever we render must round-trip as SemVer 2.0.0.
			_, err = goversion.NewSemver(ver.SemVer)
			assert.NoError(t, err, "rendered %q is not valid SemVer", ver.SemVer)
		})
	}
}

// TestDeriveTagNames pins which tag names are versions: with a v, SemVer as
// tt read it before tags without a v counted, without one SemVer 2.0.0 in
// full. A tag that is no version leaves the tagless fallback.
func TestDeriveTagNames(t *testing.T) {
	t.Parallel()

	const fallback = `^0\.0\.0-dev\.1\+g[0-9a-f]+$`

	tests := map[string]string{
		"v1.1.2":            `^1\.1\.2$`,
		"1.1.2":             `^1\.1\.2$`,
		"0.1.0":             `^0\.1\.0$`,
		"v1.2":              `^1\.2\.0$`,
		"1.2.0-rc.1":        `^1\.2\.0-rc\.1$`,
		"1.2.0+ee":          `^1\.2\.0$`,
		"1.2":               fallback,
		"1":                 fallback,
		"20261112":          fallback,
		"2026-11-12":        fallback,
		"01.2.3":            fallback,
		"1.2.3-rc.01":       fallback,
		"1.2.3.4":           fallback,
		"vfinal":            fallback,
		"v1.x":              fallback,
		"release-1.2.0":     fallback,
		"memcached-1.1.2":   fallback,
		"1.2.0-alpha.beta1": `^1\.2\.0-alpha\.beta1$`,
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := initRepo(t)
			commit(t, dir, "init")
			git(t, dir, "tag", name)

			ver, err := version.Derive(dir)
			require.NoError(t, err)
			assert.Regexp(t, want, ver.SemVer)

			if want == fallback {
				assert.Empty(t, ver.Tag)
			} else {
				assert.Equal(t, name, ver.Tag, "the tag as git has it")
			}
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
