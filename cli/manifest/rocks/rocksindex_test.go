package rocks_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/remote"
)

// indexRockspec is the rockspec of stat-<version> an index server publishes
// apart from its rocks.
func indexRockspec(version string) string {
	return `package = "stat"
version = "` + version + `"
source = { url = "https://example.test/stat.tar.gz", md5 = "0cc175b9c0f1b6a831c399e269772661" }
build = { type = "builtin", modules = {} }
`
}

// sha256Hex is the sha256 of data in hex.
func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))

	return hex.EncodeToString(sum[:])
}

// indexServer is a rock server that publishes only a tt-rocks-index.json,
// with relative URLs: a rock of every version for "all" and its rockspec
// apart, under releases/. It counts the requests of every path.
type indexServer struct {
	*httptest.Server

	mu    sync.Mutex
	files map[string]string
	hits  map[string]int
}

// newIndexServer serves versions of stat; yanked are yanked; specSHA
// overrides the digest the index lists for every rockspec when not empty.
func newIndexServer(t *testing.T, versions, yanked []string, specSHA string) *indexServer {
	t.Helper()

	server := &indexServer{files: map[string]string{}, hits: map[string]int{}}
	versionsOut := map[string]remote.IndexVersion{}

	for _, version := range versions {
		rock := "rock of stat " + version
		spec := indexRockspec(version)
		rockPath := "releases/stat-" + version + "/stat-" + version + ".all.rock"
		specPath := "releases/stat-" + version + "/stat-" + version + ".rockspec"

		server.files["/"+rockPath] = rock
		server.files["/"+specPath] = spec

		listed := sha256Hex(spec)
		if specSHA != "" {
			listed = specSHA
		}

		entry := remote.IndexVersion{
			Artifacts: []remote.IndexArtifact{{
				Arch: "all", SHA256: sha256Hex(rock), Size: int64(len(rock)), URL: rockPath,
			}},
			Rockspec: &remote.IndexFile{SHA256: listed, Size: 0, URL: specPath},
			Yanked:   false,
		}

		for _, y := range yanked {
			entry.Yanked = entry.Yanked || y == version
		}

		versionsOut[version] = entry
	}

	body, err := json.Marshal(remote.RocksIndex{
		Format:     remote.RocksIndexFormat,
		Repository: map[string]map[string]remote.IndexVersion{"stat": versionsOut},
	})
	require.NoError(t, err)

	server.files["/"+remote.RocksIndexFile] = string(body)
	server.Server = httptest.NewServer(http.HandlerFunc(server.serve))
	t.Cleanup(server.Close)

	return server
}

func (s *indexServer) serve(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()

	s.hits[request.URL.Path]++

	body, ok := s.files[request.URL.Path]

	s.mu.Unlock()

	if !ok {
		http.NotFound(writer, request)

		return
	}

	_, _ = writer.Write([]byte(body))
}

// hitsOf is the number of requests for path.
func (s *indexServer) hitsOf(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hits[path]
}

// TestResolveFromARocksIndex checks that a rock of a server that publishes a
// tt-rocks-index.json resolves with the digest of every file, and that its
// metadata comes from the rockspec the index lists apart, checked against its
// digest, without the rock being downloaded.
func TestResolveFromARocksIndex(t *testing.T) {
	t.Parallel()

	server := newIndexServer(t, []string{"0.3.1-1", "0.3.2-1"}, nil, "")
	adapter := newAdapter(server.URL + "/")

	resolved, err := adapter.Resolve(context.Background(), "stat", "", "")
	require.NoError(t, err)

	assert.Equal(t, "0.3.2-1", resolved.Version.Raw)
	assert.Equal(t, server.URL+"/releases/stat-0.3.2-1/stat-0.3.2-1.all.rock", resolved.URL)
	assert.Equal(t, sha256Hex("rock of stat 0.3.2-1"), resolved.SHA256)
	assert.Equal(t, server.URL+"/releases/stat-0.3.2-1/stat-0.3.2-1.rockspec", resolved.SpecURL)
	assert.Equal(t, sha256Hex(indexRockspec("0.3.2-1")), resolved.SpecSHA256)

	arches := make([]string, 0, len(resolved.Artifacts))
	for _, artifact := range resolved.Artifacts {
		arches = append(arches, artifact.Arch+"="+artifact.SHA256)
	}

	assert.Equal(t, []string{
		"all=" + sha256Hex("rock of stat 0.3.2-1"),
		"rockspec=" + sha256Hex(indexRockspec("0.3.2-1")),
	}, arches)

	spec, err := adapter.Metadata(context.Background(), resolved)
	require.NoError(t, err)
	assert.Equal(t, "0cc175b9c0f1b6a831c399e269772661", spec.Source.MD5)

	assert.Equal(t, 1, server.hitsOf("/releases/stat-0.3.2-1/stat-0.3.2-1.rockspec"))
	assert.Zero(t, server.hitsOf("/releases/stat-0.3.2-1/stat-0.3.2-1.all.rock"),
		"resolving must not download the rock")
}

// TestMetadataRefusesATamperedRockspec checks that a rockspec which is not the
// one the index lists is refused rather than read.
func TestMetadataRefusesATamperedRockspec(t *testing.T) {
	t.Parallel()

	server := newIndexServer(t, []string{"0.3.1-1"}, nil, strings.Repeat("0", 64))
	adapter := newAdapter(server.URL + "/")

	resolved, err := adapter.Resolve(context.Background(), "stat", "", "")
	require.NoError(t, err)

	_, err = adapter.Metadata(context.Background(), resolved)
	require.ErrorIs(t, err, luarocks.ErrChecksumMismatch)
	assert.Contains(t, err.Error(), "stat-0.3.1-1.rockspec")
}
