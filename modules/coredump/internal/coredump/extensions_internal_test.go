package coredump

import (
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
	"github.com/tarantool/tt/sdk/log/logtest"
)

// The LuaJIT of the fake GitHub, at the commits and tags of
// tarantool/tarantool of the tests.
const (
	luajitNew = "8af934d93cf26fb1e9b08b28a8b51833d55fc8e8"
	luajitOld = "a74e5be07d54b4e98b85493de73317db520b3f71"
)

// fakeGitHub serves the files of raw.githubusercontent.com and the replies
// of api.github.com its files name, by path and query, and 404 for the
// rest, and records the requests.
type fakeGitHub struct {
	mu       sync.Mutex
	files    map[string]string
	requests []string
	// status, when not 0, is the reply to every request.
	status int
}

// ServeHTTP implements http.Handler.
func (f *fakeGitHub) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	key := request.URL.Path
	if request.URL.RawQuery != "" {
		key += "?" + request.URL.RawQuery
	}

	body, ok, status := f.serve(key)

	switch {
	case status != 0:
		writer.WriteHeader(status)
	case !ok:
		http.NotFound(writer, request)
	default:
		_, _ = writer.Write([]byte(body))
	}
}

// Requests returns the requests made so far.
func (f *fakeGitHub) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.requests...)
}

// serve records the request of key and returns its file, whether there is
// one, and the status every request fails with.
func (f *fakeGitHub) serve(key string) (string, bool, int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, key)

	body, ok := f.files[key]

	return body, ok, f.status
}

// fail makes every request fail with status.
func (f *fakeGitHub) fail(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.status = status
}

// put serves body at key.
func (f *fakeGitHub) put(key, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.files[key] = body
}

// submoduleReply is the reply of the contents API for third_party/luajit at
// commit.
func submoduleReply(commit string) string {
	return `{"name": "luajit", "path": "third_party/luajit", "sha": "` + commit +
		`", "size": 0, "type": "submodule", "download_url": null}`
}

// tarantoolRepo returns the files of tarantool/tarantool at ref for the fake
// GitHub: tools/tarantool-gdb.py, which says the ref, and third_party/luajit
// at luajit.
func tarantoolRepo(ref, luajit string) map[string]string {
	return map[string]string{
		"/tarantool/tarantool/" + ref + "/tools/tarantool-gdb.py": "tarantool-gdb.py of " + ref,
		"/repos/tarantool/tarantool/contents/third_party/luajit?ref=" + ref: submoduleReply(
			luajit),
	}
}

// newFakeGitHub returns a fake GitHub with the files of tarantool/tarantool
// at 8b9a0f59a95b and 3.9.0 (LuaJIT luajitNew, of luajit_dbg.py) and at
// 3.5.0 (LuaJIT luajitOld, of luajit-gdb.py).
func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()

	files := map[string]string{
		"/tarantool/luajit/" + luajitNew + "/src/luajit_dbg.py": "luajit_dbg.py of " + luajitNew,
		"/tarantool/luajit/" + luajitOld + "/src/luajit-gdb.py": "luajit-gdb.py of " + luajitOld,
	}

	for _, repo := range []map[string]string{
		tarantoolRepo("8b9a0f59a95b", luajitNew),
		tarantoolRepo("3.9.0", luajitNew),
		tarantoolRepo("3.5.0", luajitOld),
	} {
		maps.Copy(files, repo)
	}

	github := &fakeGitHub{mu: sync.Mutex{}, files: files, requests: nil, status: 0}
	server := httptest.NewServer(github)
	t.Cleanup(server.Close)

	return github, server
}

// testResolver returns a resolver on server with the cache in cacheDir, and
// the recorder of its log.
func testResolver(t *testing.T, server *httptest.Server, cacheDir string,
) (*resolver, *logtest.Recorder) {
	t.Helper()

	log, recorder := logtest.New(t)

	return &resolver{
		log:       log,
		client:    fetch.Client{HTTP: server.Client(), Limit: extensionLimit},
		rawURL:    server.URL,
		apiURL:    server.URL,
		cacheDir:  cacheDir,
		offline:   nil,
		announced: false,
	}, recorder
}

// contents returns the names and the contents of exts.
func contents(exts []extension) map[string]string {
	got := map[string]string{}

	for _, ext := range exts {
		got[ext.Name] = string(ext.Data)
	}

	return got
}

// names returns the names of exts, in order.
func names(exts []extension) []string {
	got := make([]string, 0, len(exts))

	for _, ext := range exts {
		got = append(got, ext.Name)
	}

	return got
}

// embeddedContent returns the copy of tt of the extension name.
func embeddedContent(t *testing.T, name string) string {
	t.Helper()

	data, err := extensions.ReadFile(extensionsDir + "/" + name)
	require.NoError(t, err)

	return string(data)
}

// warnings returns the messages of the warnings recorder holds.
func warnings(recorder *logtest.Recorder) []string {
	var got []string

	for _, record := range recorder.Records() {
		if record.Level == slog.LevelWarn {
			got = append(got, record.Message)
		}
	}

	return got
}

// TestResolveCommit checks that the extensions of a Community build are
// those of its commit, the one of LuaJIT that of the commit of the
// submodule, and that they are cached by the commits: the next lookup
// makes no request.
func TestResolveCommit(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)
	cache := t.TempDir()

	res, recorder := testResolver(t, server, cache)

	exts, err := res.resolve(t.Context(), "", "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, names(exts))
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 8b9a0f59a95b",
	}, contents(exts))
	assert.Empty(t, warnings(recorder))
	assert.Equal(t, []string{
		"/repos/tarantool/tarantool/contents/third_party/luajit?ref=8b9a0f59a95b",
		"/tarantool/luajit/" + luajitNew + "/src/luajit_dbg.py",
		"/tarantool/tarantool/8b9a0f59a95b/tools/tarantool-gdb.py",
	}, github.Requests())
	assert.Contains(t, recorder.Messages(), "gdb extension luajit_dbg.py: src/luajit_dbg.py "+
		"of tarantool/luajit "+luajitNew+", the LuaJIT of tarantool/tarantool 8b9a0f59a95b")
	assert.Contains(t, recorder.Messages(), "gdb extension tarantool-gdb.py: "+
		"tools/tarantool-gdb.py of tarantool/tarantool 8b9a0f59a95b")

	assert.FileExists(t, filepath.Join(cache, "tarantool", "8b9a0f59a95b", tarantoolGDBName))
	assert.FileExists(t, filepath.Join(cache, "tarantool", "8b9a0f59a95b", "luajit"))
	assert.FileExists(t, filepath.Join(cache, "luajit", luajitNew, luajitDbgName))

	again, _ := testResolver(t, server, cache)

	exts, err = again.resolve(t.Context(), "", "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 8b9a0f59a95b",
	}, contents(exts))
	assert.Len(t, github.Requests(), 3, "the cache answers")
}

// TestResolveTag checks that the extensions of a build of a commit GitHub
// does not have are those of the tag of its release, and that nothing is
// cached by a tag.
func TestResolveTag(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)
	cache := t.TempDir()

	res, recorder := testResolver(t, server, cache)

	exts, err := res.resolve(t.Context(), "", "Tarantool 3.9.0-3-gabcdef012345")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 3.9.0",
	}, contents(exts))
	assert.Empty(t, warnings(recorder))
	assert.Equal(t, []string{
		"/repos/tarantool/tarantool/contents/third_party/luajit?ref=abcdef012345",
		"/repos/tarantool/tarantool/contents/third_party/luajit?ref=3.9.0",
		"/tarantool/luajit/" + luajitNew + "/src/luajit_dbg.py",
		"/tarantool/tarantool/abcdef012345/tools/tarantool-gdb.py",
		"/tarantool/tarantool/3.9.0/tools/tarantool-gdb.py",
	}, github.Requests())

	assert.NoDirExists(t, filepath.Join(cache, "tarantool", "3.9.0"))
	assert.FileExists(t, filepath.Join(cache, "luajit", luajitNew, luajitDbgName))
}

// TestResolveOldLuaJIT checks that the extension of a LuaJIT before
// luajit_dbg.py is luajit-gdb.py.
func TestResolveOldLuaJIT(t *testing.T) {
	t.Parallel()

	_, server := newFakeGitHub(t)

	res, recorder := testResolver(t, server, t.TempDir())

	exts, err := res.resolve(t.Context(), "", "Tarantool 3.5.0-0-g0000000")
	require.NoError(t, err)
	assert.Equal(t, []string{luajitGDBName, tarantoolGDBName}, names(exts))
	assert.Equal(t, map[string]string{
		luajitGDBName:    "luajit-gdb.py of " + luajitOld,
		tarantoolGDBName: "tarantool-gdb.py of 3.5.0",
	}, contents(exts))
	assert.Empty(t, warnings(recorder))
}

// TestResolveEnterprise checks that the extensions of Enterprise are those
// of the tag of the Community release of its version: its commit is never
// asked for.
func TestResolveEnterprise(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)

	res, recorder := testResolver(t, server, t.TempDir())

	exts, err := res.resolve(t.Context(), "", "Tarantool Enterprise 3.9.0-0-g8b9a0f59a95b-r1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 3.9.0",
	}, contents(exts))
	assert.Empty(t, warnings(recorder))

	for _, request := range github.Requests() {
		assert.NotContains(t, request, "8b9a0f59a95b")
	}
}

// TestResolveFallback checks that when GitHub cannot be asked the
// extensions are the copies of tt, each with a warning that names the
// version of the core and what the copy is of, and that a failed request
// is the last one.
func TestResolveFallback(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)
	github.fail(http.StatusForbidden)

	res, recorder := testResolver(t, server, t.TempDir())

	exts, err := res.resolve(t.Context(), "", "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		luajitDbgName:    embeddedContent(t, luajitDbgName),
		tarantoolGDBName: embeddedContent(t, tarantoolGDBName),
	}, contents(exts))
	assert.Len(t, github.Requests(), 1, "GitHub is not asked again")

	got := warnings(recorder)
	require.Len(t, got, 2)
	assert.Regexp(t, `^cannot get the luajit_dbg.py of Tarantool 3.9.0-0-g8b9a0f59a95b: `+
		`download .*: HTTP 403 Forbidden; using the one of tt, of LuaJIT 8af934d9, which may `+
		`not match the LuaJIT of the core: the lj-\* commands may fail or show wrong data$`,
		got[0])
	assert.Regexp(t, `^cannot get the tarantool-gdb.py of Tarantool 3.9.0-0-g8b9a0f59a95b: `+
		`download .*: HTTP 403 Forbidden; using the one of tt, of Tarantool 3.9.0, which may `+
		`not match the structures of the core: the tt-\* commands and the pretty-printers of `+
		`Tarantool may fail or show wrong data$`, got[1])

	// No server at all.
	server.Close()

	res, recorder = testResolver(t, server, t.TempDir())

	exts, err = res.resolve(t.Context(), "", "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, names(exts))
	assert.Len(t, warnings(recorder), 2)
}

// TestResolveNotFound checks that a version GitHub has no files of falls
// back to the copies of tt, saying where they were looked for.
func TestResolveNotFound(t *testing.T) {
	t.Parallel()

	_, server := newFakeGitHub(t)

	res, recorder := testResolver(t, server, t.TempDir())

	exts, err := res.resolve(t.Context(), "", "Tarantool 2.8.4-0-g1234567")
	require.NoError(t, err)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, names(exts))

	got := warnings(recorder)
	require.Len(t, got, 2)
	assert.Contains(t, got[0], "cannot get the luajit_dbg.py of Tarantool 2.8.4-0-g1234567: "+
		"tarantool/tarantool has no third_party/luajit at 1234567 or 2.8.4; using the one of tt")
	assert.Contains(t, got[1], "cannot get the tarantool-gdb.py of Tarantool 2.8.4-0-g1234567: "+
		"tarantool/tarantool has no tools/tarantool-gdb.py at 1234567 or 2.8.4; "+
		"using the one of tt")
}

// TestResolveNoVersion checks that a version line that is none of
// Tarantool falls back to the copies of tt with no request.
func TestResolveNoVersion(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)

	res, recorder := testResolver(t, server, t.TempDir())

	exts, err := res.resolve(t.Context(), "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, names(exts))
	assert.Empty(t, github.Requests())

	got := warnings(recorder)
	require.Len(t, got, 2)
	assert.True(t, strings.HasPrefix(got[0], "cannot get the luajit_dbg.py of the Tarantool "+
		"of the core: cannot tell the version of Tarantool from \"\""), got[0])
}

// writeSources writes files, by path, into a new directory of sources.
func writeSources(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	return dir
}

// TestResolveSources checks that the extensions of the sources come first,
// luajit_dbg.py before luajit-gdb.py, and that one the sources lack is
// looked up by the version, with a warning.
func TestResolveSources(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)

	res, recorder := testResolver(t, server, t.TempDir())
	sources := writeSources(t, map[string]string{
		"third_party/luajit/src/luajit_dbg.py": "luajit_dbg.py of the sources",
		"third_party/luajit/src/luajit-gdb.py": "luajit-gdb.py of the sources",
		"tools/tarantool-gdb.py":               "tarantool-gdb.py of the sources",
	})

	exts, err := res.resolve(t.Context(), sources, "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of the sources",
		tarantoolGDBName: "tarantool-gdb.py of the sources",
	}, contents(exts))
	assert.Empty(t, github.Requests())
	assert.Empty(t, warnings(recorder))

	res, recorder = testResolver(t, server, t.TempDir())
	sources = writeSources(t, map[string]string{
		"third_party/luajit/src/luajit-gdb.py": "luajit-gdb.py of the sources",
	})

	exts, err = res.resolve(t.Context(), sources, "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		luajitGDBName:    "luajit-gdb.py of the sources",
		tarantoolGDBName: "tarantool-gdb.py of 8b9a0f59a95b",
	}, contents(exts))
	assert.Equal(t, []string{"/tarantool/tarantool/8b9a0f59a95b/tools/tarantool-gdb.py"},
		github.Requests())
	assert.Equal(t, []string{"the sources " + sources + " have no tools/tarantool-gdb.py: " +
		"looking it up by the version of the core"}, warnings(recorder))
}

// TestResolveCacheFailure checks that a cache that cannot be written fails
// nothing.
func TestResolveCacheFailure(t *testing.T) {
	t.Parallel()

	_, server := newFakeGitHub(t)

	cache := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(cache, nil, 0o644))

	res, recorder := testResolver(t, server, cache)

	exts, err := res.resolve(t.Context(), "", "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		luajitDbgName:    "luajit_dbg.py of " + luajitNew,
		tarantoolGDBName: "tarantool-gdb.py of 8b9a0f59a95b",
	}, contents(exts))
	assert.Empty(t, warnings(recorder))
}

// TestResolveBadSubmodule checks that a reply of the contents API that is
// no submodule is a ref without one.
func TestResolveBadSubmodule(t *testing.T) {
	t.Parallel()

	github, server := newFakeGitHub(t)
	github.put("/repos/tarantool/tarantool/contents/third_party/luajit?ref=8b9a0f59a95b",
		`{"type": "dir", "sha": "`+luajitNew+`"}`)

	res, _ := testResolver(t, server, t.TempDir())

	exts, err := res.resolve(t.Context(), "", "Tarantool 3.9.0-0-g8b9a0f59a95b")
	require.NoError(t, err)
	assert.Equal(t, "luajit_dbg.py of "+luajitNew, contents(exts)[luajitDbgName],
		"the LuaJIT of the tag")
	assert.Contains(t, github.Requests(),
		"/repos/tarantool/tarantool/contents/third_party/luajit?ref=3.9.0")
}

//nolint:paralleltest // Sets the environment.
func TestDefaultCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("HOME", "/home/user")

	dir, err := os.UserCacheDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "tt", "coredump"), defaultCacheDir())
}
