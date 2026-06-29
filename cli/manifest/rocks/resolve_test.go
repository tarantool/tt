package rocks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
