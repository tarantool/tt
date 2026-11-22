package rws

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
		dataLimit:     maxDebDataSize,
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

// repositoryPackage is a package of the fake repository.
type repositoryPackage struct {
	name, version, arch string
	content             []byte
	// indexed is the content the index lists the size and the digest of;
	// nil is content.
	indexed []byte
}

// filename is where the repository has the package, as the real one names
// it.
func (pkg repositoryPackage) filename() string {
	return "pool/static/main/t/tarantool/" + pkg.name + "_" + pkg.version + "_" + pkg.arch + ".deb"
}

// repositoryIndex writes the Packages index of packages as the real
// repository does: its order of fields, a multiline description and the
// digests apt does not need here.
func repositoryIndex(packages ...repositoryPackage) string {
	var index strings.Builder

	for _, pkg := range packages {
		indexed := pkg.indexed
		if indexed == nil {
			indexed = pkg.content
		}

		digest := sha256.Sum256(indexed)

		fmt.Fprintf(&index, "Architecture: %s\n", pkg.arch)

		if pkg.name == headersPackage {
			fmt.Fprintf(&index, "Depends: tarantool (= %s)\n", pkg.version)
		}

		fmt.Fprintf(&index, "Description: Tarantool is a high performance in-memory database.\n"+
			" The package provides Tarantool.\nPackage: %s\nPriority: optional\n"+
			"Version: %s\nInstalled-Size: 1\nSize: %d\nMD5Sum: 0\nSHA1: 0\nSHA256: %s\n"+
			"Filename: %s\nFileTime: 1792748634.0\n\n",
			pkg.name, pkg.version, len(indexed), hex.EncodeToString(digest[:]), pkg.filename())
	}

	return index.String()
}

// fakeRepository serves the index of packages for each architecture and
// the packages, recording the paths requested. override, if set, answers a
// request first; it returns false to leave the request to the repository.
func fakeRepository(t *testing.T, requests *[]string, packages []repositoryPackage,
	override func(http.ResponseWriter, *http.Request) bool,
) *http.Client {
	t.Helper()

	return packageTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		*requests = append(*requests, request.URL.Path)

		if override != nil && override(writer, request) {
			return
		}

		for _, arch := range []string{debArchAMD64, debArchARM64} {
			if request.URL.Path == "/dists/static/main/binary-"+arch+"/Packages" {
				var listed []repositoryPackage

				for _, pkg := range packages {
					if pkg.arch == arch {
						listed = append(listed, pkg)
					}
				}

				_, _ = io.WriteString(writer, repositoryIndex(listed...))

				return
			}
		}

		for _, pkg := range packages {
			if request.URL.Path == "/"+pkg.filename() {
				_, _ = writer.Write(pkg.content)

				return
			}
		}

		http.NotFound(writer, request)
	})
}

// runtimeDeb and headersDeb are the packages of a build with the files
// tt needs.
func runtimeDeb(t *testing.T, entries ...debEntry) []byte {
	t.Helper()

	return debFixture(t, append([]debEntry{
		{name: "./", kind: tar.TypeDir, mode: 0o755},
		{name: "./usr/", kind: tar.TypeDir, mode: 0o755},
		{name: "./usr/bin/", kind: tar.TypeDir, mode: 0o755},
		{name: "./usr/bin/tarantool", mode: 0o755, data: "executable"},
	}, entries...)...)
}

func headersDeb(t *testing.T) []byte {
	t.Helper()

	return debFixture(t,
		debEntry{name: "./usr/include/tarantool/", kind: tar.TypeDir, mode: 0o755},
		debEntry{name: "./usr/include/tarantool/module.h", mode: 0o644, data: "headers"},
		debEntry{name: "./usr/share/doc/tarantool-dev/LICENSE", mode: 0o644, data: "dev license"},
	)
}

// buildPackages are the packages of the build of version for arch.
func buildPackages(version, arch string, runtime, headers []byte) []repositoryPackage {
	return []repositoryPackage{
		{name: runtimePackage, version: version, arch: arch, content: runtime, indexed: nil},
		{name: headersPackage, version: version, arch: arch, content: headers, indexed: nil},
	}
}

func TestListAndFetch(t *testing.T) {
	for goarch, debArch := range map[string]string{"amd64": "amd64", "arm64": "arm64"} {
		t.Run(goarch, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)

			var requests []string

			other := map[string]string{"amd64": "arm64", "arm64": "amd64"}[debArch]
			packages := append(append(
				buildPackages("3.7.1-1", debArch, runtimeDeb(t), headersDeb(t)),
				buildPackages("3.7.1-2", other, []byte("other"), []byte("other"))...),
				buildPackages("3.7.0-1", debArch, []byte("older"), []byte("older"))...)
			source := packageTestSource(t, fakeRepository(t, &requests, packages, nil))

			artifacts, err := source.List(t.Context(), packageTestQuery(goarch, "3.7.1"))
			require.NoError(t, err)
			require.Len(t, artifacts, 1)
			assert.Equal(t, "3.7.1", artifacts[0].Version)
			assert.Equal(t, "tarantool_3.7.1-1_"+debArch+".tar.gz", artifacts[0].Name)

			index := "/dists/static/main/binary-" + debArch + "/Packages"
			require.Equal(t, []string{index}, requests)

			var archive bytes.Buffer

			require.NoError(t, source.Fetch(t.Context(), artifacts[0], &archive))
			require.Equal(t, []string{
				index, "/" + packages[0].filename(), "/" + packages[1].filename(),
			}, requests)
			assert.Equal(t, []archiveTestEntry{
				{"tarantool", tar.TypeReg, 0o755, "executable"},
				{"include/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/module.h", tar.TypeReg, 0o644, "headers"},
			}, readTestArchive(t, archive.Bytes()))

			entries, err := os.ReadDir(temp)
			require.NoError(t, err)
			require.Empty(t, entries, "the packages and their files are removed")
		})
	}
}

// TestFetchKeepsShareAndLicenses keeps the share/tarantool of the runtime
// package, the EmmyLua stubs of Tarantool 3.9 and later, and its license
// files, as the debs of rws.tarantool.org lay them out; the rest of
// usr/share is left out.
func TestFetchKeepsShareAndLicenses(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []debEntry
		want    []archiveTestEntry
	}{
		{
			name: "deb",
			entries: []debEntry{
				{name: "./usr/share/doc/tarantool/AUTHORS", mode: 0o644, data: "authors"},
				{name: "./usr/share/doc/tarantool/LICENSE", mode: 0o644, data: "license"},
				{name: "./usr/share/doc/tarantool/README.md", mode: 0o644, data: "readme"},
				{name: "./usr/share/doc/tarantool/copyright", mode: 0o644, data: "copyright"},
				{name: "./usr/share/man/man1/tarantool.1", mode: 0o644, data: "man"},
				{name: "./usr/share/tarantool/emmylua/checks.lua", mode: 0o644, data: "checks"},
				{name: "./usr/share/tarantool/emmylua/metrics.lua", mode: 0o644, data: "metrics"},
				{
					name: "./usr/share/tarantool/outside", kind: tar.TypeSymlink,
					data: "../../outside",
				},
			},
			want: []archiveTestEntry{
				{"LICENSE", tar.TypeReg, 0o644, "license"},
				{"AUTHORS", tar.TypeReg, 0o644, "authors"},
				{"include/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/module.h", tar.TypeReg, 0o644, "headers"},
				{"share/", tar.TypeDir, 0o755, ""},
				{"share/tarantool/", tar.TypeDir, 0o755, ""},
				{"share/tarantool/emmylua/", tar.TypeDir, 0o755, ""},
				{"share/tarantool/emmylua/checks.lua", tar.TypeReg, 0o644, "checks"},
				{"share/tarantool/emmylua/metrics.lua", tar.TypeReg, 0o644, "metrics"},
			},
		},
		{
			name: "licenses before the documents",
			entries: []debEntry{
				{name: "./usr/share/licenses/tarantool/LICENSE", mode: 0o644, data: "license"},
				{name: "./usr/share/doc/tarantool/LICENSE", mode: 0o644, data: "doc license"},
				{name: "./usr/share/doc/tarantool/AUTHORS", mode: 0o644, data: "doc authors"},
			},
			want: []archiveTestEntry{
				{"LICENSE", tar.TypeReg, 0o644, "license"},
				{"include/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/", tar.TypeDir, 0o755, ""},
				{"include/tarantool/module.h", tar.TypeReg, 0o644, "headers"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())

			var requests []string

			source := packageTestSource(t, fakeRepository(t, &requests, buildPackages(
				"3.9.0-1", debArchAMD64, runtimeDeb(t, test.entries...), headersDeb(t)), nil))

			artifacts, err := source.List(t.Context(), packageTestQuery("amd64", "3.9.0"))
			require.NoError(t, err)
			require.Len(t, artifacts, 1)

			var archive bytes.Buffer

			require.NoError(t, source.Fetch(t.Context(), artifacts[0], &archive))
			assert.Equal(t, append([]archiveTestEntry{
				{"tarantool", tar.TypeReg, 0o755, "executable"},
			}, test.want...), readTestArchive(t, archive.Bytes()))
		})
	}
}

func TestListAndFetchErrors(t *testing.T) {
	tests := []struct {
		name       string
		indexCode  int
		index      string
		fileCode   int
		indexLimit int64
		fileLimit  int64
		// runtime and headers replace the packages of the build; indexed
		// replaces what the index lists the size and digest of.
		runtime   []byte
		headers   []byte
		indexed   []byte
		listError bool
		wantIs    error
		wantError string
	}{
		{name: "index unavailable", indexCode: 404, listError: true,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 404"},
		{name: "index server error", indexCode: 500, listError: true,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 500"},
		{name: "index limit", indexLimit: 12, listError: true,
			wantIs: download.ErrTooLarge, wantError: "index exceeds 12 bytes"},
		{name: "no index", index: "<html><body>Moved</body></html>\n", listError: true,
			wantIs: errInvalidIndex, wantError: "read Tarantool package index " + packageTestURL +
				"/dists/static/main/binary-amd64/Packages: invalid package index: " +
				"line 1 is no field"},
		{name: "package unavailable", fileCode: 404,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 404"},
		{name: "package server error", fileCode: 500,
			wantIs: download.ErrHTTPStatus, wantError: "HTTP 500"},
		{name: "package limit", fileLimit: 12,
			wantIs: download.ErrTooLarge, wantError: "exceeds 12 bytes"},
		{name: "checksum mismatch", indexed: []byte("another package"), wantIs: errChecksum,
			wantError: "has SHA-256 "},
		{name: "corrupt package", runtime: []byte("invalid deb"), wantIs: errInvalidDeb,
			wantError: "extract Tarantool package " + packageTestURL + "/pool/"},
		{name: "unsupported compression", runtime: func() []byte {
			members := debMembers(t, debEntry{name: "./usr/bin/tarantool", mode: 0o755})

			members[2].name = "data.tar.zst"

			return arFixture(members...)
		}(), wantIs: errInvalidDeb, wantError: `unsupported deb data compression "data.tar.zst"`},
		{name: "missing executable",
			runtime: debFixture(t, debEntry{name: "./usr/bin/another", mode: 0o755, data: "x"}),
			wantIs:  errPackageContents, wantError: "no executable usr/bin/tarantool"},
		{name: "executable not executable",
			runtime: debFixture(t, debEntry{name: "./usr/bin/tarantool", mode: 0o644, data: "x"}),
			wantIs:  errPackageContents, wantError: "no executable usr/bin/tarantool"},
		{name: "missing headers",
			headers: debFixture(t, debEntry{name: "./usr/include/tarantool/lua.h", mode: 0o644}),
			wantIs:  errPackageContents, wantError: "no usr/include/tarantool/module.h"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)

			runtime, headers := runtimeDeb(t), headersDeb(t)
			if test.runtime != nil {
				runtime = test.runtime
			}

			if test.headers != nil {
				headers = test.headers
			}

			packages := buildPackages("3.7.1-1", debArchAMD64, runtime, headers)

			packages[0].indexed = test.indexed

			var requests []string

			client := fakeRepository(t, &requests, packages, func(
				writer http.ResponseWriter, request *http.Request,
			) bool {
				isIndex := strings.HasSuffix(request.URL.Path, "/Packages")

				switch {
				case isIndex && test.indexCode != 0:
					writer.WriteHeader(test.indexCode)
				case isIndex && test.index != "":
					_, _ = io.WriteString(writer, test.index)
				case !isIndex && test.fileCode != 0:
					writer.WriteHeader(test.fileCode)
				default:
					return false
				}

				return true
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
			require.ErrorIs(t, err, test.wantIs)
			require.NotErrorIs(t, err, bundle.ErrNotFound,
				"a package the index names is no build the repository lacks")

			entries, err := os.ReadDir(temp)
			require.NoError(t, err)
			require.Empty(t, entries, "failed downloads must remove partial payloads")
		})
	}
}

// TestFetchChecksumMessage names the package, both digests and both sizes.
func TestFetchChecksumMessage(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	runtime := runtimeDeb(t)
	packages := buildPackages("3.7.1-1", debArchAMD64, runtime, headersDeb(t))

	packages[0].indexed = append(bytes.Clone(runtime), 0)

	var requests []string

	source := packageTestSource(t, fakeRepository(t, &requests, packages, nil))

	artifacts, err := source.List(t.Context(), packageTestQuery("amd64", "3.7.1"))
	require.NoError(t, err)
	require.Len(t, artifacts, 1)

	actual, listed := sha256.Sum256(runtime), sha256.Sum256(packages[0].indexed)

	err = source.Fetch(t.Context(), artifacts[0], io.Discard)
	require.EqualError(t, err, fmt.Sprintf("package checksum mismatch: Tarantool package "+
		"%s/%s has SHA-256 %s and %d bytes, the package index lists %s and %d bytes; "+
		"use --source-build to build from source", packageTestURL, packages[0].filename(),
		hex.EncodeToString(actual[:]), len(runtime), hex.EncodeToString(listed[:]),
		len(runtime)+1))
}

func TestFetchInvalidLocator(t *testing.T) {
	file := `{"url":"https://packages.test/a.deb","size":1,"sha256":"` +
		strings.Repeat("a", sha256Length) + `"}`
	empty := strings.Replace(file, `"size":1`, `"size":0`, 1)

	for _, reference := range []string{
		"", "https://packages.test/a.deb", `{"runtime":` + file + `}`,
		`{"runtime":"https://packages.test/a.deb","headers":"https://packages.test/b.deb"}`,
		`{"runtime":` + file + `,"headers":` + empty + `}`,
		`{"runtime":` + file + `,"headers":` + strings.ReplaceAll(file, "a", "A") + `}`,
	} {
		err := New().Fetch(t.Context(), bundle.Artifact{Locator: reference}, io.Discard)
		require.ErrorIs(t, err, errInvalidLocator, reference)
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

			err := writeArchive(&archive, archiveContents{
				executable: executable, headers: headers, share: "", licenses: nil,
			})
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
