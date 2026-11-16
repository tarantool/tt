package registry_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/v3/cli/manifest/registry"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// helloRockspec is the rockspec of hello 1.0.0-1 whose sources are the git
// repository at repo, at its tag 1.0.0.
const helloRockspec = `package = "hello"
version = "1.0.0-1"
source = { url = "git+file://%s", tag = "1.0.0" }
build = { type = "builtin", modules = { hello = "hello.lua" } }
`

// lockPreamble is the preamble every lock of the tests needs to parse.
const lockPreamble = `lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0'
manifest_hash = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
`

// zipBytes is a zip archive of the files, name to body.
func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	archive := zip.NewWriter(&buf)

	for _, name := range slices.Sorted(maps.Keys(files)) {
		entry, err := archive.Create(name)
		require.NoError(t, err)

		_, err = entry.Write([]byte(files[name]))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())

	return buf.Bytes()
}

// bareRepo makes a bare git repository whose tag 1.0.0 has hello.lua return
// 'tagged' and whose later commit has it return 'head', and returns its path.
// The test is skipped without git.
func bareRepo(t *testing.T) string {
	t.Helper()

	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found in PATH")
	}

	work := filepath.Join(t.TempDir(), "work")
	bare := filepath.Join(t.TempDir(), "hello.git")

	git := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)

		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")

		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}

	commit := func(body string) {
		require.NoError(t, os.WriteFile(filepath.Join(work, "hello.lua"), []byte(body), 0o600))
		git("-C", work, "add", "hello.lua")
		git("-C", work, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", body)
	}

	git("init", "-q", work)
	commit("return 'tagged'\n")
	git("-C", work, "tag", "1.0.0")
	commit("return 'head'\n")
	git("clone", "-q", "--bare", work, bare)

	return bare
}

// rockServer serves the files over HTTP, with a LuaRocks manifest listing
// each rock file under the arch manifest gives, and returns its URL.
func rockServer(t *testing.T, files map[string][]byte, manifest string) string {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), body, 0o600))
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest"), []byte(manifest), 0o600))

	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(server.Close)

	return server.URL + "/"
}

// rockspecServer serves hello 1.0.0-1 as a rockspec alone, its sources the
// git repository at repo.
func rockspecServer(t *testing.T, repo string) string {
	t.Helper()

	return rockServer(t, map[string][]byte{
		"hello-1.0.0-1.rockspec": []byte(fmt.Sprintf(helloRockspec, repo)),
	}, `repository = { hello = { ["1.0.0-1"] = { { arch = "rockspec" } } } }`)
}

// download mirrors opts.Refs, or the lock of opts.ProjectDir, from server
// into a fresh directory, returning the directory and what was written.
func download(
	t *testing.T, server string, opts registry.DownloadOptions,
) (string, []string, error) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "mirror")

	opts.Dir = dir
	opts.Registries = []rocks.Registry{{URL: server, Source: rocks.SourceFlag}}
	opts.Tarantool = rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.5.0"}

	written, err := registry.Download(context.Background(), opts)

	return dir, written, err
}

// zipEntries reads the files of the zip archive at path, name to body.
func zipEntries(t *testing.T, path string) map[string]string {
	t.Helper()

	archive, err := zip.OpenReader(path)
	require.NoError(t, err)

	defer func() { _ = archive.Close() }()

	entries := map[string]string{}

	for _, file := range archive.File {
		opened, err := file.Open()
		require.NoError(t, err)

		body, err := io.ReadAll(opened)
		require.NoError(t, err)
		require.NoError(t, opened.Close())

		entries[file.Name] = string(body)
	}

	return entries
}

// indexedArches returns the arches tt-rocks-index.json of dir lists hello
// 1.0.0-1 under, and whether it lists its rockspec apart.
func indexedArches(t *testing.T, dir string) ([]string, bool) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(dir, "tt-rocks-index.json"))
	require.NoError(t, err)

	var index remote.RocksIndex

	require.NoError(t, json.Unmarshal(body, &index))

	version := index.Repository["hello"]["1.0.0-1"]
	arches := make([]string, 0, len(version.Artifacts))

	for _, artifact := range version.Artifacts {
		arches = append(arches, artifact.Arch)
	}

	slices.Sort(arches)

	return arches, version.Rockspec != nil
}

// requirePacked checks that dir holds hello's rockspec and the source rock
// packed of it, with the sources at the tag, and indexes both.
func requirePacked(t *testing.T, dir string, written []string) {
	t.Helper()

	srcRock := filepath.Join(dir, "hello-1.0.0-1.src.rock")
	assert.Equal(t, []string{filepath.Join(dir, "hello-1.0.0-1.rockspec"), srcRock}, written)

	spec, err := os.ReadFile(filepath.Join(dir, "hello-1.0.0-1.rockspec"))
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"hello-1.0.0-1.rockspec": string(spec),
		"hello/hello.lua":        "return 'tagged'\n",
	}, zipEntries(t, srcRock), "the rockspec and the clone at the tag, without .git")

	arches, hasSpec := indexedArches(t, dir)
	assert.Equal(t, []string{"src"}, arches)
	assert.True(t, hasSpec, "the rockspec stays listed apart")
}

func TestDownloadPacksTheSourcesOfARockspec(t *testing.T) {
	t.Parallel()

	repo := bareRepo(t)
	server := rockspecServer(t, repo)

	dir, written, err := download(t, server, registry.DownloadOptions{
		Refs: []registry.Ref{{Name: "hello", Version: "1.0.0-1"}},
	})
	require.NoError(t, err)
	requirePacked(t, dir, written)

	// The same sources pack into the same bytes, so re-running keeps the
	// digests a lock pins of the mirror.
	first, err := os.ReadFile(written[1])
	require.NoError(t, err)

	_, again, err := download(t, server, registry.DownloadOptions{
		Refs: []registry.Ref{{Name: "hello"}},
	})
	require.NoError(t, err)

	second, err := os.ReadFile(again[1])
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestDownloadPacksTheDevClosure(t *testing.T) {
	t.Parallel()

	server := rockspecServer(t, bareRepo(t))
	project := t.TempDir()

	lock := lockPreamble + `
[[lock.dev_dependencies]]
name = 'hello'
version = '1.0.0-1'
source = 'registry'
`
	lockPath := filepath.Join(project, "app.manifest.lock")
	require.NoError(t, os.WriteFile(lockPath, []byte(lock), 0o600))

	dir, written, err := download(t, server, registry.DownloadOptions{ProjectDir: project})
	require.NoError(t, err)
	requirePacked(t, dir, written)
}

func TestDownloadFailsWhenTheSourcesAreGone(t *testing.T) {
	t.Parallel()

	repo := bareRepo(t)
	server := rockspecServer(t, repo)
	require.NoError(t, os.RemoveAll(repo))

	dir, _, err := download(t, server, registry.DownloadOptions{
		Refs: []registry.Ref{{Name: "hello", Version: "1.0.0-1"}},
	})
	require.ErrorContains(t, err, "downloading hello@1.0.0-1: packing its sources")
	assert.NoFileExists(t, filepath.Join(dir, "hello-1.0.0-1.src.rock"))
}

func TestDownloadKeepsARockAsItIs(t *testing.T) {
	t.Parallel()

	// A source rock the server has is downloaded, not packed anew: its
	// rockspec names sources that are not there.
	srcRock := zipBytes(t, map[string]string{
		"hello-1.0.0-1.rockspec": fmt.Sprintf(helloRockspec, "/nonexistent/hello.git"),
		"hello/hello.lua":        "return 'served'\n",
	})
	server := rockServer(t, map[string][]byte{
		"hello-1.0.0-1.src.rock": srcRock,
		"hello-1.0.0-1.rockspec": []byte(fmt.Sprintf(helloRockspec, "/nonexistent/hello.git")),
	}, `repository = { hello = { ["1.0.0-1"] = { { arch = "src" }, { arch = "rockspec" } } } }`)

	dir, written, err := download(t, server, registry.DownloadOptions{
		Refs: []registry.Ref{{Name: "hello", Version: "1.0.0-1"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "hello-1.0.0-1.src.rock")}, written)

	got, err := os.ReadFile(written[0])
	require.NoError(t, err)
	assert.Equal(t, srcRock, got)
}
