package rocks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rockDir writes a directory rock server holding files, each empty.
func rockDir(t *testing.T, files ...string) string {
	t.Helper()

	dir := t.TempDir()

	for _, file := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), nil, 0o600))
	}

	return dir
}

// rockHTTP serves a rock server holding files, answering status to the
// requests of every other path.
func rockHTTP(t *testing.T, status int, files ...string) string {
	t.Helper()

	has := map[string]bool{}
	for _, file := range files {
		has["/rocks/"+file] = true
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if has[r.URL.Path] {
			return
		}

		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)

	return server.URL + "/rocks"
}

func TestIndexProbe(t *testing.T) {
	t.Parallel()

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	tests := []struct {
		name   string
		server string
		want   bool
	}{
		{"a directory with the index alone", rockDir(t, "tt-rocks-index.json"), true},
		{"a directory as a file:// URL", "file://" + rockDir(t, "tt-rocks-index.json"), true},
		{"a directory with a manifest too", rockDir(t, "tt-rocks-index.json", "manifest"), false},
		{"a directory with manifest-5.1", rockDir(t, "tt-rocks-index.json", "manifest-5.1"), false},
		{"a directory with a manifest alone", rockDir(t, "manifest"), false},
		{"a directory that is not there", filepath.Join(t.TempDir(), "none"), false},
		{"an HTTP server with the index alone", rockHTTP(t, http.StatusNotFound,
			"tt-rocks-index.json"), true},
		{"an S3 bucket with the index alone", rockHTTP(t, http.StatusForbidden,
			"tt-rocks-index.json"), true},
		{"an HTTP server with a manifest too", rockHTTP(t, http.StatusNotFound,
			"tt-rocks-index.json", "manifest-5.1.zip"), false},
		{"an HTTP server without the index", rockHTTP(t, http.StatusNotFound, "manifest"),
			false},
		{"an HTTP server that answers an error", rockHTTP(t, http.StatusInternalServerError,
			"tt-rocks-index.json"), false},
		{"an HTTP server that does not answer", closed.URL, false},
		{"a git server", "git://example.test/rocks", false},
	}

	probe := newIndexProbe()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, probe.indexOnly(context.Background(), test.server))
		})
	}
}

func TestKeepManifestServers(t *testing.T) {
	t.Parallel()

	index := rockDir(t, "tt-rocks-index.json")
	classic := rockDir(t, "manifest")

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	got := keepManifestServers(context.Background(), newIndexProbe(),
		[]string{index, classic, closed.URL})

	// The HTTP server does not answer at all, and is kept for LuaRocks to
	// report on.
	assert.Equal(t, []string{classic, closed.URL}, got)
}

func TestCheckNamedServers(t *testing.T) {
	t.Parallel()

	index := rockDir(t, "tt-rocks-index.json")
	classic := rockDir(t, "manifest")
	probe := newIndexProbe()

	for _, args := range [][]string{
		{"install", "foo", "--server", index},
		{"install", "foo", "--server=" + classic + " " + index},
		{"--only-server", index, "install", "foo"},
		{"install", "--only-from=" + index, "foo"},
		{"install", "--from", index, "foo"},
	} {
		err := checkNamedServers(context.Background(), probe, args)
		require.ErrorIs(t, err, ErrIndexOnlyServer, args)
		assert.Contains(t, err.Error(), index)
		assert.Contains(t, err.Error(), "tt package")
	}

	for _, args := range [][]string{
		{"install", "foo", "--server", classic},
		{"install", "foo", "--", "--server", index},
		{"install", "foo", "--server"},
		{"search", index},
	} {
		require.NoError(t, checkNamedServers(context.Background(), probe, args), args)
	}
}
