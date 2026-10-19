package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

type ttArchiveEntry struct {
	name    string
	mode    int64
	kind    byte
	content string
}

func ttPackageTestArchive(t *testing.T, entries ...ttArchiveEntry) []byte {
	t.Helper()

	var contents bytes.Buffer

	compressed := gzip.NewWriter(&contents)
	archive := tar.NewWriter(compressed)

	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Mode: entry.mode, Typeflag: entry.kind,
			Size: int64(len(entry.content)),
		}
		if entry.kind == tar.TypeSymlink || entry.kind == tar.TypeLink {
			header.Linkname = "../outside"
		}

		require.NoError(t, archive.WriteHeader(header))

		_, err := archive.Write([]byte(entry.content))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())

	return contents.Bytes()
}

func ttPackageTestDownloader(server *httptest.Server) ttPackageDownloader {
	return ttPackageDownloader{
		client:    server.Client(),
		fileLimit: maxPackageSize, keepTemp: false,
		releasesURL: server.URL + "/releases/download/",
	}
}

func TestPreparePrebuiltTt(t *testing.T) {
	for _, platform := range []struct{ os, arch string }{
		{osLinux, archAMD64}, {osLinux, archARM64}, {osDarwin, archAMD64},
	} {
		for _, version := range []string{"v2.15.0", "2.15.0", "v3.0.0-rc1"} {
			t.Run(platform.os+"/"+platform.arch+"/"+version, func(t *testing.T) {
				var requests []string

				archive := ttPackageTestArchive(t,
					ttArchiveEntry{
						name: "README.md", kind: tar.TypeReg, mode: 0o644, content: "readme",
					},
					ttArchiveEntry{
						name: "./tt", kind: tar.TypeReg, mode: 0o755, content: "executable",
					})

				name := fmt.Sprintf("tt-%s-%s-%s.tar.gz", strings.TrimPrefix(version, "v"),
					platform.os, platform.arch)
				downloadPath := "/releases/download/" + version + "/" + name
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
					request *http.Request,
				) {
					requests = append(requests, request.URL.Path)
					switch request.URL.Path {
					case downloadPath:
						http.Redirect(writer, request, "/asset", http.StatusFound)
					case "/asset":
						_, _ = writer.Write(archive)
					default:
						t.Errorf("unexpected download request: %s", request.URL.Path)
						writer.WriteHeader(http.StatusNotFound)
					}
				}))

				defer server.Close()

				destination := filepath.Join(t.TempDir(), "new-directory")
				downloader := ttPackageTestDownloader(server)
				artifacts, err := downloader.prepare(version, destination,
					platform.os, platform.arch)
				require.NoError(t, err)
				assert.Equal(t, binary.ProgramTt, artifacts.program)
				assert.True(t, filepath.IsAbs(artifacts.executable))
				assert.Empty(t, artifacts.headers)
				assert.Equal(t, []string{downloadPath, "/asset"}, requests)

				contents, err := os.ReadFile(artifacts.executable)
				require.NoError(t, err)
				assert.Equal(t, "executable", string(contents))

				info, err := os.Stat(artifacts.executable)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

				files, err := os.ReadDir(filepath.Dir(artifacts.executable))
				require.NoError(t, err)
				require.Len(t, files, 1)
				assert.Equal(t, "tt", files[0].Name())
			})
		}
	}
}

func TestPreparePrebuiltTtUnsupportedPlatform(t *testing.T) {
	for _, test := range []struct{ os, arch, version string }{
		{osDarwin, archARM64, "v2.15.0"}, {osLinux, "386", "v2.15.0"},
		{"freebsd", archAMD64, "v2.15.0"}, {osLinux, archAMD64, "master"},
	} {
		t.Run(test.os+"/"+test.arch+"/"+test.version, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter,
				_ *http.Request,
			) {
				t.Error("unsupported platform or version must not make HTTP requests")
			}))
			defer server.Close()

			downloader := ttPackageTestDownloader(server)
			_, err := downloader.prepare(test.version, t.TempDir(), test.os, test.arch)
			require.ErrorIs(t, err, errPrebuiltPackageUnavailable)
			require.ErrorContains(t, err, "--source-build")
		})
	}
}

func TestPreparePrebuiltTtErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		fileLimit int64
		chunked   bool
		keepTemp  bool
		want      error
	}{
		{name: "missing archive", status: 404, want: errPrebuiltPackageUnavailable},
		{name: "missing archive with no-clean", status: 404, keepTemp: true,
			want: errPrebuiltPackageUnavailable},
		{name: "forbidden", status: 403, want: errPackageHTTPStatus},
		{name: "rate limit", status: 429, want: errPackageHTTPStatus},
		{name: "server error", status: 500, want: errPackageHTTPStatus},
		{name: "asset limit", fileLimit: 12, want: errPackageTooLarge},
		{name: "chunked asset limit", fileLimit: 12, chunked: true, want: errPackageTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := ttPackageTestArchive(t,
				ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "executable"})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
				request *http.Request,
			) {
				assert.Equal(t,
					"/releases/download/v2.15.0/tt-2.15.0-linux-amd64.tar.gz", request.URL.Path)

				if test.status != 0 {
					writer.WriteHeader(test.status)

					return
				}

				if test.chunked {
					flusher, ok := writer.(http.Flusher)
					assert.True(t, ok)
					flusher.Flush()
				}

				_, _ = writer.Write(archive)
			}))

			defer server.Close()

			downloader := ttPackageTestDownloader(server)

			downloader.keepTemp = test.keepTemp

			if test.fileLimit != 0 {
				downloader.fileLimit = test.fileLimit
			}

			destination := t.TempDir()
			_, err := downloader.prepare("v2.15.0", destination, osLinux, archAMD64)
			require.ErrorIs(t, err, test.want)

			files, err := os.ReadDir(destination)
			require.NoError(t, err)
			assert.Empty(t, files, "failed delivery should clean its temporary artifacts")
		})
	}
}

func TestExtractTtPackageRejectsInvalidArchives(t *testing.T) {
	valid := ttPackageTestArchive(t,
		ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "executable"})
	badChecksum := bytes.Clone(valid)

	badChecksum[len(badChecksum)-8] ^= 1

	for _, test := range []struct {
		name    string
		archive []byte
	}{
		{"invalid gzip", []byte("broken")},
		{"truncated gzip", valid[:len(valid)-4]},
		{"bad gzip checksum", badChecksum},
		{"missing executable", ttPackageTestArchive(t,
			ttArchiveEntry{name: "README", kind: tar.TypeReg, mode: 0o644, content: "readme"})},
		{"not executable", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o644, content: "executable"})},
		{"directory", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeDir, mode: 0o755})},
		{"duplicate binary", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "one"},
			ttArchiveEntry{name: "./tt", kind: tar.TypeReg, mode: 0o755, content: "two"})},
		{"symlink", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeSymlink, mode: 0o755})},
		{"hardlink", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeLink, mode: 0o755})},
		{"parent traversal", ttPackageTestArchive(t,
			ttArchiveEntry{name: "../outside", kind: tar.TypeReg, mode: 0o755, content: "bad"})},
		{"absolute path", ttPackageTestArchive(t,
			ttArchiveEntry{name: "/outside", kind: tar.TypeReg, mode: 0o755, content: "bad"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			archivePath := filepath.Join(root, "package.tar.gz")
			require.NoError(t, os.WriteFile(archivePath, test.archive, 0o600))

			destination := filepath.Join(root, "extract")
			require.NoError(t, os.Mkdir(destination, 0o755))

			_, err := extractTtPackage(archivePath, destination)
			require.ErrorIs(t, err, errPackageContents)
			require.NotErrorIs(t, err, errPrebuiltPackageUnavailable)
			assert.NoFileExists(t, filepath.Join(root, "outside"))
		})
	}
}
