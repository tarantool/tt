package search_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/binary/search"
)

// git runs git in dir and returns its output, trimmed, failing the test on an
// error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	args = append([]string{
		"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com",
	}, args...)

	out, err := exec.CommandContext(context.Background(), "git", args...).CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)

	return strings.TrimSpace(string(out))
}

// isolateGit keeps the configuration of the user and of the system out of the
// git the test and the code under test run.
func isolateGit(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not in PATH")
	}

	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// emptyCommit records an empty commit in dir and returns its hash.
func emptyCommit(t *testing.T, dir, message string) string {
	t.Helper()

	git(t, dir, "commit", "-q", "--allow-empty", "-m", message)

	return git(t, dir, "rev-parse", "HEAD")
}

// lookupRemote is a remote repository whose default branch, v3, is not master,
// as of tarantool/tt, with a branch master, a tag on master and a pull request.
type lookupRemote struct {
	url string
	// master and v3 are the heads of the branches, tag the commit of the tag
	// 1.0.0 and pull the head of pull request 7.
	master, v3, tag, pull string
}

func newLookupRemote(t *testing.T) lookupRemote {
	t.Helper()

	dir := t.TempDir()
	git(t, dir, "init", "-q", "--initial-branch=master")

	remote := lookupRemote{url: "file://" + dir}

	remote.tag = emptyCommit(t, dir, "first")
	git(t, dir, "tag", "1.0.0")

	remote.master = emptyCommit(t, dir, "master only")

	git(t, dir, "switch", "-q", "-c", "v3", remote.tag)

	remote.v3 = emptyCommit(t, dir, "v3")
	remote.pull = emptyCommit(t, dir, "pull request")

	git(t, dir, "update-ref", "refs/pull/7/head", remote.pull)
	git(t, dir, "reset", "-q", "--hard", remote.v3)

	return remote
}

// TestGetCommitFromGitRemote resolves every branch of a remote whose default
// branch is not master, and its tags, commits and pull requests.
func TestGetCommitFromGitRemote(t *testing.T) {
	isolateGit(t)

	remote := newLookupRemote(t)

	for ref, want := range map[string]string{
		"master":             remote.master,
		"v3":                 remote.v3,
		"1.0.0":              remote.tag,
		remote.master[:7]:    remote.master,
		remote.v3:            remote.v3,
		remote.tag[:10]:      remote.tag,
		"refs/heads/master":  remote.master,
		"refs/tags/1.0.0":    remote.tag,
		"master~1":           remote.tag,
		"v3^":                remote.tag,
		"refs/heads/v3~1":    remote.tag,
		remote.master + "~1": remote.tag,
	} {
		t.Run(ref, func(t *testing.T) {
			commit, err := search.GetCommitFromGitRemote(remote.url, ref)
			require.NoError(t, err)
			assert.Equal(t, want, commit)
		})
	}

	_, err := search.GetCommitFromGitRemote(remote.url, "no-such-branch")
	require.ErrorContains(t, err, "failed to find commit no-such-branch")

	commit, err := search.GetPullRequestCommitFromGitRemote(remote.url, "7")
	require.NoError(t, err)
	assert.Equal(t, remote.pull, commit)
}

// TestGetCommitFromGitLocal resolves a branch a clone has only from its remote,
// as git checkout of the build from the clone takes it, and keeps the branch
// of the clone where it has one.
func TestGetCommitFromGitLocal(t *testing.T) {
	isolateGit(t)

	remote := newLookupRemote(t)

	clone := t.TempDir()
	git(t, clone, "clone", "-q", remote.url, ".")
	require.Equal(t, "v3", git(t, clone, "branch", "--show-current"))

	commit, err := search.GetCommitFromGitLocal(clone, "master")
	require.NoError(t, err)
	assert.Equal(t, remote.master, commit)

	commit, err = search.GetCommitFromGitLocal(clone, "v3")
	require.NoError(t, err)
	assert.Equal(t, remote.v3, commit)

	// A branch of the clone is taken before the one of its remote.
	git(t, clone, "branch", "master", remote.tag)

	commit, err = search.GetCommitFromGitLocal(clone, "master")
	require.NoError(t, err)
	assert.Equal(t, remote.tag, commit)

	// A branch two remotes have is ambiguous, as for git checkout.
	git(t, clone, "branch", "-q", "-D", "master")
	git(t, clone, "remote", "add", "other", remote.url)
	git(t, clone, "fetch", "-q", "other")

	_, err = search.GetCommitFromGitLocal(clone, "master")
	require.ErrorContains(t, err, "failed to find commit master")
}
