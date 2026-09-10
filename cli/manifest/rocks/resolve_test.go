package rocks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/rocks"
)

// manifestServer starts a fake rock server that serves a single JSON manifest
// (manifest-5.1.json) built from repo, the (name → version → arch) shape
// HTTPRemoteIndex consumes. hits counts every request the server receives, so
// tests can assert which servers were consulted.
func manifestServer(t *testing.T, body string, hits *int32) *httptest.Server {
	t.Helper()

	handler := func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(hits, 1)

		if strings.HasSuffix(request.URL.Path, "/manifest-5.1.json") {
			_, _ = writer.Write([]byte(body))

			return
		}

		http.NotFound(writer, request)
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)

	return server
}

// repoJSON renders a one-rock manifest body for name at the given versions,
// each advertised as a rockspec arch.
func repoJSON(name string, versions ...string) string {
	entries := make([]string, 0, len(versions))
	for _, version := range versions {
		entries = append(entries, `"`+version+`":[{"arch":"rockspec"}]`)
	}

	return `{"repository":{"` + name + `":{` + strings.Join(entries, ",") + `}}}`
}

// newAdapter builds an adapter whose ordered server list is exactly servers.
func newAdapter(servers ...string) *rocks.Adapter {
	return rocks.New(rocks.BuildConfig(rocks.TarantoolInfo{
		Executable: "tarantool",
		Prefix:     "/usr",
		Version:    "3.1.0",
	}, rocks.ConfigOptions{
		Tree:       "/app/.rocks",
		WorkingDir: "/app",
		Servers:    servers,
		Logger:     nil,
	}))
}

func TestResolveFirstServerWins(t *testing.T) {
	t.Parallel()

	var hits1, hits2 int32

	first := manifestServer(t, repoJSON("metrics", "1.0.0-1"), &hits1)
	second := manifestServer(t, repoJSON("metrics", "2.0.0-1"), &hits2)

	adapter := newAdapter(first.URL, second.URL)

	resolved, err := adapter.Resolve(context.Background(), "metrics", "", "")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(resolved.URL, first.URL),
		"want %s under %s", resolved.URL, first.URL)
	assert.Equal(t, "1.0.0-1", resolved.Version.Raw)
	// First server had the rock, so the second is never consulted.
	assert.Zero(t, atomic.LoadInt32(&hits2))
}

func TestResolveSecondServerWhenFirstMissing(t *testing.T) {
	t.Parallel()

	var hits1, hits2 int32

	first := manifestServer(t, repoJSON("other", "1.0.0-1"), &hits1)
	second := manifestServer(t, repoJSON("metrics", "2.0.0-1"), &hits2)

	adapter := newAdapter(first.URL, second.URL)

	resolved, err := adapter.Resolve(context.Background(), "metrics", "", "")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(resolved.URL, second.URL))
	assert.Equal(t, "2.0.0-1", resolved.Version.Raw)
	// First server was consulted and lacked the rock, so it was queried.
	assert.Positive(t, atomic.LoadInt32(&hits1))
}

func TestResolveRegistryOverride(t *testing.T) {
	t.Parallel()

	var hits1, hits2 int32

	first := manifestServer(t, repoJSON("metrics", "1.0.0-1"), &hits1)
	second := manifestServer(t, repoJSON("metrics", "2.0.0-1"), &hits2)

	adapter := newAdapter(first.URL, second.URL)

	// The registry override pins the second server; the default list is ignored.
	resolved, err := adapter.Resolve(context.Background(), "metrics", "", second.URL)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(resolved.URL, second.URL))
	assert.Equal(t, "2.0.0-1", resolved.Version.Raw)
	assert.Zero(t, atomic.LoadInt32(&hits1), "registry override must not query the default servers")
}

func TestResolveConstraintPicksNewestMatch(t *testing.T) {
	t.Parallel()

	var hits int32

	server := manifestServer(t, repoJSON("metrics", "1.0.0-1", "2.0.0-1"), &hits)

	adapter := newAdapter(server.URL)

	resolved, err := adapter.Resolve(context.Background(), "metrics", "<2.0.0", "")
	require.NoError(t, err)

	assert.Equal(t, "1.0.0-1", resolved.Version.Raw)
}

// TestResolveSkipsMovingVersions: LuaRocks orders scm above every number, so an
// ordinary lower bound would otherwise select the development branch and pin a
// lock to code that changes underneath it.
func TestResolveSkipsMovingVersions(t *testing.T) {
	t.Parallel()

	var hits int32

	server := manifestServer(t, repoJSON("metrics", "1.0.0-1", "2.0.0-1", "scm-1"), &hits)

	adapter := newAdapter(server.URL)

	resolved, err := adapter.Resolve(context.Background(), "metrics", ">=1.0.0", "")
	require.NoError(t, err)

	assert.Equal(t, "2.0.0-1", resolved.Version.Raw)
}

// TestResolveTakesMovingWhenAskedFor: naming the branch is how a project asks
// for it on purpose, and that request must still be honoured.
func TestResolveTakesMovingWhenAskedFor(t *testing.T) {
	t.Parallel()

	var hits int32

	server := manifestServer(t, repoJSON("metrics", "1.0.0-1", "scm-1"), &hits)

	adapter := newAdapter(server.URL)

	resolved, err := adapter.Resolve(context.Background(), "metrics", "==scm", "")
	require.NoError(t, err)

	assert.Equal(t, "scm-1", resolved.Version.Raw)
}

// TestResolveFallsBackToMoving: a rock published only as a branch is the
// ordinary state for much of the ecosystem, so refusing it would resolve
// nothing at all.
func TestResolveFallsBackToMoving(t *testing.T) {
	t.Parallel()

	var hits int32

	server := manifestServer(t, repoJSON("metrics", "scm-1"), &hits)

	adapter := newAdapter(server.URL)

	resolved, err := adapter.Resolve(context.Background(), "metrics", ">=1.0.0", "")
	require.NoError(t, err)

	assert.Equal(t, "scm-1", resolved.Version.Raw)
}

// TestResolveSkipsMovingOutOfRange: the stable pass narrows the candidates, it
// does not widen them - a released version outside the range stays refused
// rather than being replaced by the branch.
func TestResolveSkipsMovingOutOfRange(t *testing.T) {
	t.Parallel()

	var hits int32

	server := manifestServer(t, repoJSON("metrics", "1.0.0-1", "scm-1"), &hits)

	adapter := newAdapter(server.URL)

	_, err := adapter.Resolve(context.Background(), "metrics", ">=2.0.0", "")
	assert.ErrorIs(t, err, rocks.ErrNoMatch)
}

func TestResolveNotFound(t *testing.T) {
	t.Parallel()

	var hits int32

	server := manifestServer(t, repoJSON("other", "1.0.0-1"), &hits)

	adapter := newAdapter(server.URL)

	_, err := adapter.Resolve(context.Background(), "metrics", "", "")
	assert.ErrorIs(t, err, rocks.ErrNotFound)
}

func TestResolveNoMatch(t *testing.T) {
	t.Parallel()

	var hits int32

	server := manifestServer(t, repoJSON("metrics", "1.0.0-1"), &hits)

	adapter := newAdapter(server.URL)

	_, err := adapter.Resolve(context.Background(), "metrics", ">=2.0.0", "")
	assert.ErrorIs(t, err, rocks.ErrNoMatch)
}

// localRock is the rock name the directory-server tests publish.
const localRock = "stat"

// localRepo writes a directory rock server: a Lua-source `manifest` listing
// localRock at every version as an "all" arch, plus the .all.rock file each
// row points at. This is the shape `luarocks admin make_manifest` produces,
// which is what makes a downloaded mirror usable as a server.
func localRepo(t *testing.T, versions ...string) string {
	t.Helper()

	dir := t.TempDir()

	entries := make([]string, 0, len(versions))

	for _, version := range versions {
		entries = append(entries,
			`      ["`+version+`"] = {`+"\n"+
				`         { arch = "all" }`+"\n"+
				`      }`)

		rock := filepath.Join(dir, localRock+"-"+version+".all.rock")
		require.NoError(t, os.WriteFile(rock, []byte("rock"), 0o600))
	}

	body := "commands = {}\nmodules = {}\nrepository = {\n   " + localRock + " = {\n" +
		strings.Join(entries, ",\n") + "\n   }\n}\n"

	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest"), []byte(body), 0o600))

	return dir
}

func TestResolveLocalDirectoryServer(t *testing.T) {
	t.Parallel()

	dir := localRepo(t, "0.3.1-1", "0.3.2-1")

	adapter := newAdapter(dir)

	resolved, err := adapter.Resolve(context.Background(), "stat", "", "")
	require.NoError(t, err)

	assert.Equal(t, "0.3.2-1", resolved.Version.Raw)
	assert.Equal(t, filepath.Join(dir, "stat-0.3.2-1.all.rock"), resolved.URL)
}

func TestResolveLocalDirectoryBeforeHTTP(t *testing.T) {
	t.Parallel()

	var hits int32

	dir := localRepo(t, "0.3.1-1")
	server := manifestServer(t, repoJSON("stat", "9.9.9-1"), &hits)

	adapter := newAdapter(dir, server.URL)

	resolved, err := adapter.Resolve(context.Background(), "stat", "", "")
	require.NoError(t, err)

	// A directory and an HTTP server compose in one ordered list: the
	// directory answers first, so the network is never touched.
	assert.Equal(t, "0.3.1-1", resolved.Version.Raw)
	assert.Zero(t, atomic.LoadInt32(&hits))
}

func TestResolveRegistryOverrideLocalDirectory(t *testing.T) {
	t.Parallel()

	var hits int32

	dir := localRepo(t, "0.3.1-1")
	server := manifestServer(t, repoJSON("stat", "9.9.9-1"), &hits)

	adapter := newAdapter(server.URL)

	// A per-dependency registry may name a directory as well as a URL.
	resolved, err := adapter.Resolve(context.Background(), "stat", "", dir)
	require.NoError(t, err)

	assert.Equal(t, "0.3.1-1", resolved.Version.Raw)
	assert.Zero(t, atomic.LoadInt32(&hits))
}

func TestResolveFileURLServer(t *testing.T) {
	t.Parallel()

	dir := localRepo(t, "0.3.1-1")

	adapter := newAdapter("file://" + dir)

	resolved, err := adapter.Resolve(context.Background(), "stat", "", "")
	require.NoError(t, err)

	assert.Equal(t, "0.3.1-1", resolved.Version.Raw)
}

func TestResolveNoServersConfigured(t *testing.T) {
	t.Parallel()

	adapter := rocks.New(rocks.BuildConfig(rocks.TarantoolInfo{
		Executable: "tarantool",
		Prefix:     "/usr",
		Version:    "3.1.0",
	}, rocks.ConfigOptions{
		Tree:       "/app/.rocks",
		WorkingDir: "/app",
		Servers:    []string{},
		Logger:     nil,
	}))

	// An empty server list is a configuration error, not "no such rock":
	// answering ErrNotFound would send the caller hunting a missing rock.
	_, err := adapter.Resolve(context.Background(), "stat", "", "")
	require.Error(t, err)
	require.NotErrorIs(t, err, rocks.ErrNotFound)
	assert.Contains(t, err.Error(), "no servers configured")
}
