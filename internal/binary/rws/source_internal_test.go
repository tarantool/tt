package rws

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	encodingbinary "encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/internal/binary/download"
	"github.com/ulikunitz/xz"
)

const packageTestURL = "https://packages.test"

type packageTestTransport func(*http.Request) (*http.Response, error)

func (transport packageTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// packageTestClient handles requests in memory, including redirects and streamed bodies.
func packageTestClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()

	return &http.Client{Transport: packageTestTransport(func(request *http.Request) (
		*http.Response, error,
	) {
		require.Equal(t, "packages.test", request.URL.Host)

		writer := httptest.NewRecorder()

		handler(writer, request)

		response := writer.Result()

		response.Request = request

		if !writer.Flushed && response.Header.Get("Content-Length") == "" {
			response.ContentLength = int64(writer.Body.Len())
		}

		return response, nil
	})}
}

// packageTestSource is the repository at packageTestURL on a host with glibc.
func packageTestSource(t *testing.T, client *http.Client) *Source {
	t.Helper()

	return &Source{
		client:        download.Client{HTTP: client, FileLimit: download.FileLimit},
		repositoryURL: packageTestURL,
		indexLimit:    download.IndexLimit,
		systemRoot:    packagePlatformFixture(t),
	}
}

func packageTestQuery(goarch, version string) bundle.Query {
	return bundle.Query{
		Product: product, OS: goosLinux, Arch: goarch,
		Channel: bundle.ChannelRelease, Version: version,
	}
}

// recordLogs records what the test logs.
func recordLogs(t *testing.T) *logtest.Recorder {
	t.Helper()

	logger, recorder := logtest.New(t)
	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	return recorder
}

func requireLogged(t *testing.T, recorder *logtest.Recorder, text string) {
	t.Helper()

	for _, message := range recorder.Messages() {
		if strings.Contains(message, text) {
			return
		}
	}

	require.Failf(t, "not logged", "no message contains %q: %q", text, recorder.Messages())
}

func packageTestLocator(t *testing.T, artifact bundle.Artifact) locator {
	t.Helper()

	var packages locator

	require.NoError(t, json.Unmarshal([]byte(artifact.Locator), &packages))

	return packages
}

func TestSelectPackages(t *testing.T) {
	const packagesURL = packageTestURL + "/x86_64/Packages/"

	tests := []struct {
		name    string
		files   []string
		line    string
		want    map[string][2]string
		message string
	}{
		{
			name: "exact tag and newest numeric RPM revision",
			files: []string{
				"tarantool-3.7.1-2.x86_64.rpm", "tarantool-devel-3.7.1-2.x86_64.rpm",
				"tarantool-3.7.1-10.x86_64.rpm", "tarantool-devel-3.7.1-10.x86_64.rpm",
				"tarantool-3.7.10-99.x86_64.rpm", "tarantool-3.7.1-99.aarch64.rpm",
				"tarantool-debuginfo-3.7.1-99.x86_64.rpm", "tarantool-3.7.1-beta-99.x86_64.rpm",
			},
			line: "3.7.1",
			want: map[string][2]string{"3.7.1": {
				"tarantool-3.7.1-10.x86_64.rpm", "tarantool-devel-3.7.1-10.x86_64.rpm",
			}},
		},
		{
			name:  "missing runtime",
			files: []string{"tarantool-devel-3.7.1-1.x86_64.rpm"},
			line:  "3.7.1",
		},
		{
			name: "missing matching newest headers does not downgrade",
			files: []string{
				"tarantool-3.7.1-1.x86_64.rpm", "tarantool-devel-3.7.1-1.x86_64.rpm",
				"tarantool-3.7.1-2.x86_64.rpm",
			},
			line:    "3.7.1",
			message: "no matching headers package tarantool-devel-3.7.1-2.x86_64.rpm",
		},
		{
			name: "release line",
			files: []string{
				"tarantool-3.7.0-1.x86_64.rpm", "tarantool-devel-3.7.0-1.x86_64.rpm",
				"tarantool-3.7.1-1.x86_64.rpm", "tarantool-devel-3.7.1-1.x86_64.rpm",
				"tarantool-3.8.0-1.x86_64.rpm", "tarantool-devel-3.8.0-1.x86_64.rpm",
				"tarantool-3.70.0-1.x86_64.rpm", "tarantool-devel-3.70.0-1.x86_64.rpm",
			},
			line: "3.7",
			want: map[string][2]string{
				"3.7.0": {"tarantool-3.7.0-1.x86_64.rpm", "tarantool-devel-3.7.0-1.x86_64.rpm"},
				"3.7.1": {"tarantool-3.7.1-1.x86_64.rpm", "tarantool-devel-3.7.1-1.x86_64.rpm"},
			},
		},
		{
			name: "every version",
			files: []string{
				"tarantool-3.7.0-1.x86_64.rpm", "tarantool-devel-3.7.0-1.x86_64.rpm",
				"tarantool-3.8.0-1.x86_64.rpm", "tarantool-devel-3.8.0-1.x86_64.rpm",
			},
			want: map[string][2]string{
				"3.7.0": {"tarantool-3.7.0-1.x86_64.rpm", "tarantool-devel-3.7.0-1.x86_64.rpm"},
				"3.8.0": {"tarantool-3.8.0-1.x86_64.rpm", "tarantool-devel-3.8.0-1.x86_64.rpm"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := recordLogs(t)

			files := make(map[string]struct{})
			for _, file := range test.files {
				files[file] = struct{}{}
			}

			got := make(map[string][2]string)

			for _, artifact := range selectPackages(files, packagesURL, test.line, "x86_64") {
				packages := packageTestLocator(t, artifact)

				assert.Empty(t, artifact.Flavor)
				assert.Empty(t, artifact.SHA256, "the repository has no digests")
				assert.Equal(t, strings.TrimSuffix(filepath.Base(packages.Runtime), ".rpm")+
					".tar.gz", artifact.Name)
				require.True(t, strings.HasPrefix(packages.Runtime, packagesURL))
				require.True(t, strings.HasPrefix(packages.Headers, packagesURL))

				got[artifact.Version] = [2]string{
					strings.TrimPrefix(packages.Runtime, packagesURL),
					strings.TrimPrefix(packages.Headers, packagesURL),
				}
			}

			if test.want == nil {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, test.want, got)
			}

			if test.message != "" {
				requireLogged(t, recorder, test.message)
			}
		})
	}
}

func TestListAndFetch(t *testing.T) {
	for goarch, rpmArch := range map[string]string{"amd64": "x86_64", "arm64": "aarch64"} {
		t.Run(goarch, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)

			var requests []string

			runtimeName := "tarantool-3.7.1-1." + rpmArch + ".rpm"
			headersName := "tarantool-devel-3.7.1-1." + rpmArch + ".rpm"
			runtimeRPM := packageTestRPM(t, "./usr/bin/tarantool", "executable", 0100755)
			headersRPM := packageTestRPM(t, "./usr/include/tarantool/module.h", "headers", 0100644)
			client := packageTestClient(t, func(
				writer http.ResponseWriter, request *http.Request,
			) {
				requests = append(requests, request.URL.Path)
				switch filepath.Base(request.URL.Path) {
				case "Packages":
					_, _ = fmt.Fprintf(writer, `<a href="%s">runtime</a><a href="%s">headers</a>`,
						runtimeName, headersName)
				case runtimeName:
					_, _ = writer.Write(runtimeRPM)
				case headersName:
					_, _ = writer.Write(headersRPM)
				default:
					http.NotFound(writer, request)
				}
			})

			source := packageTestSource(t, client)
			artifacts, err := source.List(t.Context(), packageTestQuery(goarch, "3.7.1"))
			require.NoError(t, err)
			require.Len(t, artifacts, 1)
			assert.Equal(t, "3.7.1", artifacts[0].Version)
			require.Equal(t, []string{"/" + rpmArch + "/Packages/"}, requests)

			var archive bytes.Buffer

			require.NoError(t, source.Fetch(t.Context(), artifacts[0], &archive))
			require.Equal(t, []string{
				"/" + rpmArch + "/Packages/", "/" + rpmArch + "/Packages/" + runtimeName,
				"/" + rpmArch + "/Packages/" + headersName,
			}, requests)
			assert.Equal(t, []archiveTestEntry{
				{"tarantool", tar.TypeReg, 0o755, "executable"},
				{"include/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/module.h", tar.TypeReg, 0o644, "headers"},
			}, readTestArchive(t, archive.Bytes()))

			entries, err := os.ReadDir(temp)
			require.NoError(t, err)
			require.Empty(t, entries, "the RPMs and their files are removed")
		})
	}
}

func TestListAndFetchErrors(t *testing.T) {
	tests := []struct {
		name       string
		indexCode  int
		fileCode   int
		indexLimit int64
		fileLimit  int64
		invalidRPM bool
		noBinary   bool
		noHeaders  bool
		listError  bool
		wantIs     error
		wantError  string
	}{
		{name: "index unavailable", indexCode: 404, listError: true,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 404"},
		{name: "index server error", indexCode: 500, listError: true,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 500"},
		{name: "index limit", indexLimit: 12, listError: true,
			wantIs: download.ErrTooLarge, wantError: "index exceeds 12 bytes"},
		{name: "package unavailable", fileCode: 404,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 404"},
		{name: "package server error", fileCode: 500,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 500"},
		{name: "package limit", fileLimit: 12,
			wantIs: download.ErrTooLarge, wantError: "exceeds 12 bytes"},
		{name: "corrupt package", invalidRPM: true, wantError: "extract Tarantool package"},
		{name: "missing executable", noBinary: true, wantIs: errPackageContents,
			wantError: "no executable usr/bin/tarantool"},
		{name: "missing headers", noHeaders: true, wantIs: errPackageContents,
			wantError: "no usr/include/tarantool/module.h"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)

			runtimeRPM := packageTestRPM(t, "./usr/bin/tarantool", "executable", 0100755)
			if test.noBinary {
				runtimeRPM = packageTestRPM(t, "./usr/bin/another", "executable", 0100755)
			}

			headersRPM := packageTestRPM(t, "./usr/include/tarantool/module.h", "headers", 0100644)
			if test.noHeaders {
				headersRPM = packageTestRPM(t, "./usr/include/tarantool/lua.h", "headers", 0100644)
			}

			client := packageTestClient(t, func(
				writer http.ResponseWriter, request *http.Request,
			) {
				if strings.HasSuffix(request.URL.Path, "/Packages/") {
					if test.indexCode != 0 {
						writer.WriteHeader(test.indexCode)

						return
					}

					_, _ = fmt.Fprint(writer, `<a href="tarantool-3.7.1-1.x86_64.rpm">runtime</a>`+
						`<a href="tarantool-devel-3.7.1-1.x86_64.rpm">headers</a>`)

					return
				}

				switch {
				case test.fileCode != 0:
					writer.WriteHeader(test.fileCode)
				case test.invalidRPM:
					_, _ = fmt.Fprint(writer, "invalid RPM")
				case strings.Contains(request.URL.Path, "devel"):
					_, _ = writer.Write(headersRPM)
				default:
					_, _ = writer.Write(runtimeRPM)
				}
			})

			source := packageTestSource(t, client)
			if test.indexLimit != 0 {
				source.indexLimit = test.indexLimit
			}

			if test.fileLimit != 0 {
				source.client.FileLimit = test.fileLimit
			}

			artifacts, err := source.List(t.Context(), packageTestQuery("amd64", "3.7.1"))
			if test.listError {
				require.ErrorContains(t, err, test.wantError)
				require.ErrorIs(t, err, test.wantIs)

				return
			}

			require.NoError(t, err)
			require.Len(t, artifacts, 1)

			err = source.Fetch(t.Context(), artifacts[0], io.Discard)
			require.ErrorContains(t, err, test.wantError)
			require.NotErrorIs(t, err, bundle.ErrNotFound,
				"an RPM the listing names is no build the repository lacks")

			if test.wantIs != nil {
				require.ErrorIs(t, err, test.wantIs)
			}

			entries, err := os.ReadDir(temp)
			require.NoError(t, err)
			require.Empty(t, entries, "failed downloads must remove partial payloads")
		})
	}
}

func TestFetchInvalidLocator(t *testing.T) {
	for _, reference := range []string{"", "https://packages.test/a.rpm", `{"runtime":"a"}`} {
		err := New().Fetch(t.Context(), bundle.Artifact{Locator: reference}, io.Discard)
		require.ErrorIs(t, err, errInvalidLocator)
	}
}

func TestWriteArchiveLinks(t *testing.T) {
	for _, test := range []struct {
		name   string
		link   string
		target string
		want   error
	}{
		{name: "file inside", link: "lua.hpp", target: "lua.h"},
		{name: "file outside", link: "outside.h", target: "../../outside.h",
			want: errPackageContents},
		{name: "directory", link: "alias", target: "curl", want: errPackageContents},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "tarantool")
			headers := filepath.Join(root, "usr", "include", "tarantool")

			require.NoError(t, os.WriteFile(executable, []byte("executable"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(headers, "curl"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "outside.h"), nil, 0o644))

			for name, content := range map[string]string{
				"module.h": "module", "lua.h": "lua", "curl/curl.h": "curl",
			} {
				require.NoError(t,
					os.WriteFile(filepath.Join(headers, name), []byte(content), 0o644))
			}

			require.NoError(t, os.Symlink(test.target, filepath.Join(headers, test.link)))

			var archive bytes.Buffer

			err := writeArchive(&archive, executable, headers)
			if test.want != nil {
				require.ErrorIs(t, err, test.want)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, []archiveTestEntry{
				{"tarantool", tar.TypeReg, 0o755, "executable"},
				{"include/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/curl/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/curl/curl.h", tar.TypeReg, 0o644, "curl"},
				{"include/tarantool/lua.h", tar.TypeReg, 0o644, "lua"},
				{"include/tarantool/lua.hpp", tar.TypeReg, 0o644, "lua"},
				{"include/tarantool/module.h", tar.TypeReg, 0o644, "module"},
			}, readTestArchive(t, archive.Bytes()))
		})
	}
}

type archiveTestEntry struct {
	name    string
	kind    byte
	mode    int64
	content string
}

func readTestArchive(t *testing.T, data []byte) []archiveTestEntry {
	t.Helper()

	compressed, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)

	reader := tar.NewReader(compressed)

	var entries []archiveTestEntry

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)

		content, err := io.ReadAll(reader)
		require.NoError(t, err)

		entries = append(entries, archiveTestEntry{
			name: header.Name, kind: header.Typeflag, mode: header.Mode, content: string(content),
		})
	}

	return entries
}

func TestComparePackageReleases(t *testing.T) {
	for _, pair := range [][2]string{
		{"1", "2"}, {"2", "10"}, {"1.el8", "1.el9"}, {"1~rc1", "1"},
		{"1", "1^git1"}, {"1^git1", "1.1"}, {"1a", "1.1"}, {"1.9", "1.10"},
	} {
		require.Negative(t, comparePackageReleases(pair[0], pair[1]), "%s < %s", pair[0], pair[1])
		require.Positive(t, comparePackageReleases(pair[1], pair[0]))
	}

	for _, pair := range [][2]string{{"1", "01"}, {"1.1", "1+1"}, {"1", "1"}, {"", ""}} {
		require.Zero(t, comparePackageReleases(pair[0], pair[1]))
	}
}

// packageTestRPM writes a minimal XZ-compressed CPIO RPM so delivery tests
// exercise the real extractor without external tools.
func packageTestRPM(t *testing.T, name, content string, mode uint32) []byte {
	t.Helper()

	var payload bytes.Buffer

	for _, entry := range []struct {
		name, content string
		mode          uint32
	}{{name, content, mode}, {"TRAILER!!!", "", 0}} {
		fmt.Fprintf(&payload, "070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
			1, entry.mode, 0, 0, 1, 0, len(entry.content), 0, 0, 0, 0, len(entry.name)+1, 0)
		payload.WriteString(entry.name + "\x00")

		for payload.Len()%4 != 0 {
			payload.WriteByte(0)
		}

		payload.WriteString(entry.content)

		for payload.Len()%4 != 0 {
			payload.WriteByte(0)
		}
	}

	var archive bytes.Buffer

	lead := make([]byte, 96)
	copy(lead, []byte{0xed, 0xab, 0xee, 0xdb, 3})
	archive.Write(lead)

	headerMagic := []byte{0x8e, 0xad, 0xe8, 1, 0, 0, 0, 0}
	archive.Write(headerMagic)
	archive.Write(make([]byte, 8)) // Empty signature header, already aligned to 8 bytes.
	archive.Write(headerMagic)

	_ = encodingbinary.Write(&archive, encodingbinary.BigEndian, uint32(2))
	_ = encodingbinary.Write(&archive, encodingbinary.BigEndian, uint32(len("cpio\x00xz\x00")))

	for _, entry := range [][4]uint32{{1124, 6, 0, 1}, {1125, 6, 5, 1}} {
		_ = encodingbinary.Write(&archive, encodingbinary.BigEndian, entry)
	}

	archive.WriteString("cpio\x00xz\x00")

	writer, err := (xz.WriterConfig{DictCap: 1 << 20}).NewWriter(&archive)
	require.NoError(t, err)

	_, err = writer.Write(payload.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return archive.Bytes()
}
