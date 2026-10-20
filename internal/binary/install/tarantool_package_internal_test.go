package install

import (
	"bytes"
	encodingbinary "encoding/binary"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/ulikunitz/xz"
)

func TestTarantoolPackageSelection(t *testing.T) {
	tests := []struct {
		name      string
		files     []string
		binary    string
		headers   string
		wantError string
	}{
		{
			name: "exact tag and newest numeric RPM revision",
			files: []string{
				"tarantool-3.7.1-2.x86_64.rpm", "tarantool-devel-3.7.1-2.x86_64.rpm",
				"tarantool-3.7.1-10.x86_64.rpm", "tarantool-devel-3.7.1-10.x86_64.rpm",
				"tarantool-3.7.10-99.x86_64.rpm", "tarantool-3.7.1-99.aarch64.rpm",
				"tarantool-debuginfo-3.7.1-99.x86_64.rpm", "tarantool-3.7.1-beta-99.x86_64.rpm",
			},
			binary: "tarantool-3.7.1-10.x86_64.rpm", headers: "tarantool-devel-3.7.1-10.x86_64.rpm",
		},
		{
			name:      "missing runtime",
			files:     []string{"tarantool-devel-3.7.1-1.x86_64.rpm"},
			wantError: "no prebuilt Tarantool package",
		},
		{
			name: "missing matching newest headers does not downgrade",
			files: []string{
				"tarantool-3.7.1-1.x86_64.rpm", "tarantool-devel-3.7.1-1.x86_64.rpm",
				"tarantool-3.7.1-2.x86_64.rpm",
			},
			wantError: "no matching headers package tarantool-devel-3.7.1-2.x86_64.rpm",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := packageTestClient(t, func(
				writer http.ResponseWriter, _ *http.Request,
			) {
				for _, file := range test.files {
					_, _ = fmt.Fprintf(writer, `<a href="/Packages/%s">package</a>`, file)
				}
			})

			downloader := packageTestDownloader(t, client)
			binName, incName, err := downloader.selectPackages(packageTestURL, "3.7.1", "x86_64")

			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				require.ErrorContains(t, err, "--source-build")
			} else {
				require.NoError(t, err)
				require.Equal(t, test.binary, binName)
				require.Equal(t, test.headers, incName)
			}
		})
	}
}

func TestPreparePrebuiltTarantool(t *testing.T) {
	for goarch, rpmArch := range map[string]string{"amd64": "x86_64", "arm64": "aarch64"} {
		t.Run(goarch, func(t *testing.T) {
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

			destination := filepath.Join(t.TempDir(), "new-directory")
			downloader := packageTestDownloader(t, client)
			artifacts, err := downloader.prepare("3.7.1", destination, goarch)
			require.NoError(t, err)
			require.Equal(t, binary.ProgramCe, artifacts.program)
			require.True(t, filepath.IsAbs(artifacts.executable))
			require.True(t, filepath.IsAbs(artifacts.headers))

			binData, err := os.ReadFile(artifacts.executable)
			require.NoError(t, err)
			require.Equal(t, "executable", string(binData))

			headerData, err := os.ReadFile(filepath.Join(artifacts.headers, "module.h"))
			require.NoError(t, err)
			require.Equal(t, "headers", string(headerData))
			require.Equal(t, []string{
				"/" + rpmArch + "/Packages/", "/" + rpmArch + "/Packages/" + runtimeName,
				"/" + rpmArch + "/Packages/" + headersName,
			}, requests)

			runtimeRoot := filepath.Dir(filepath.Dir(filepath.Dir(artifacts.executable)))
			archives, err := filepath.Glob(filepath.Join(runtimeRoot, "*.rpm"))
			require.NoError(t, err)
			require.Empty(t, archives)
		})
	}
}

func TestPreparePrebuiltTarantoolErrors(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		goarch     string
		indexCode  int
		fileCode   int
		indexLimit int64
		fileLimit  int64
		invalidRPM bool
		noBinary   bool
		wantError  string
	}{
		{name: "unsupported architecture", goarch: "386", wantError: "--source-build"},
		{name: "unsupported release series", version: "2.11.8", wantError: "--source-build"},
		{name: "unresolved version", version: "master", wantError: "--source-build"},
		{name: "index unavailable", indexCode: 404, wantError: "HTTP 404"},
		{name: "index server error", indexCode: 500, wantError: "HTTP 500"},
		{name: "index limit", indexLimit: 12, wantError: "index exceeds 12 bytes"},
		{name: "package unavailable", fileCode: 404, wantError: "HTTP 404"},
		{name: "package server error", fileCode: 500, wantError: "HTTP 500"},
		{name: "package limit", fileLimit: 12, wantError: "exceeds 12 bytes"},
		{name: "corrupt package", invalidRPM: true, wantError: "extract Tarantool package"},
		{name: "missing executable", noBinary: true, wantError: "no executable usr/bin/tarantool"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtimeRPM := packageTestRPM(t, "./usr/bin/tarantool", "executable", 0100755)
			if test.noBinary {
				runtimeRPM = packageTestRPM(t, "./usr/bin/another", "executable", 0100755)
			}

			requests := 0
			client := packageTestClient(t, func(
				writer http.ResponseWriter, request *http.Request,
			) {
				requests++

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
					data := packageTestRPM(t, "./usr/include/tarantool/module.h",
						"headers", 0100644)

					_, _ = writer.Write(data)
				default:
					_, _ = writer.Write(runtimeRPM)
				}
			})

			downloader := packageTestDownloader(t, client)
			if test.indexLimit != 0 {
				downloader.indexLimit = test.indexLimit
			}

			if test.fileLimit != 0 {
				downloader.fileLimit = test.fileLimit
			}

			version, goarch := test.version, test.goarch
			if version == "" {
				version = "3.7.1"
			}

			if goarch == "" {
				goarch = "amd64"
			}

			destination := t.TempDir()
			_, err := downloader.prepare(version, destination, goarch)
			require.ErrorContains(t, err, test.wantError)

			entries, err := os.ReadDir(destination)
			require.NoError(t, err)
			require.Empty(t, entries, "failed downloads must remove partial payloads")

			if test.goarch == "386" || test.version != "" {
				require.Zero(t, requests)
			}
		})
	}
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

func packageTestDownloader(t *testing.T, client *http.Client) tarantoolPackageDownloader {
	t.Helper()

	return tarantoolPackageDownloader{
		client: client, indexLimit: maxPackageIndexSize,
		fileLimit: maxPackageSize, keepTemp: false,
		repositoryURL: packageTestURL,
		systemRoot:    packagePlatformFixture(t),
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
