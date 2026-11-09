package ghrelease

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func packageTestSource(client *http.Client) *Source {
	return &Source{
		client:      download.Client{HTTP: client, FileLimit: download.FileLimit},
		releasesURL: packageTestURL + "/releases/download/",
		latestURL:   packageTestURL + "/releases/latest",
	}
}

func packageTestQuery(goos, goarch, version string) bundle.Query {
	return bundle.Query{
		Product: product, OS: goos, Arch: goarch,
		Channel: bundle.ChannelRelease, Version: version,
	}
}

// packageTestChecksums lists the archive among other release files, as goreleaser does.
func packageTestChecksums(name string, archive []byte) string {
	return fmt.Sprintf("%x  tt_2.15.0_amd64.deb\n%x  %s\n", sha256.Sum256(nil),
		sha256.Sum256(archive), name)
}

// packageTestArchive stands for an archive: the source does not read it.
var packageTestArchive = []byte("archive of tt")

func TestListAndFetch(t *testing.T) {
	for _, platform := range []struct {
		os, arch string
		versions []string
	}{
		{goosLinux, goarchAMD64, []string{"v2.15.0", "2.15.0", "v3.0.0-rc1"}},
		{goosLinux, goarchARM64, []string{"v2.15.0", "2.15.0", "v3.0.0-rc1"}},
		{goosDarwin, goarchAMD64, []string{"v2.15.0", "2.15.0"}},
		{goosDarwin, goarchARM64, []string{"v3.0.0-rc1", "3.0.0", "v3.1.0"}},
	} {
		for _, version := range platform.versions {
			t.Run(platform.os+"/"+platform.arch+"/"+version, func(t *testing.T) {
				var requests []string

				name := fmt.Sprintf("tt-%s-%s-%s.tar.gz", strings.TrimPrefix(version, "v"),
					platform.os, platform.arch)
				downloadPath := "/releases/download/" + version + "/" + name
				checksumsPath := "/releases/download/" + version + "/checksums.txt"
				client := packageTestClient(t, func(writer http.ResponseWriter,
					request *http.Request,
				) {
					requests = append(requests, request.URL.Path)
					switch request.URL.Path {
					case downloadPath:
						http.Redirect(writer, request, "/asset", http.StatusFound)
					case "/asset":
						_, _ = writer.Write(packageTestArchive)
					case checksumsPath:
						_, _ = writer.Write([]byte(packageTestChecksums(name, packageTestArchive)))
					default:
						t.Errorf("unexpected download request: %s", request.URL.Path)
						writer.WriteHeader(http.StatusNotFound)
					}
				})

				source := packageTestSource(client)
				artifacts, err := source.List(t.Context(),
					packageTestQuery(platform.os, platform.arch, version))
				require.NoError(t, err)
				require.Len(t, artifacts, 1)
				assert.Equal(t, strings.TrimPrefix(version, "v"), artifacts[0].Version)
				assert.Empty(t, artifacts[0].Flavor)
				assert.Equal(t, name, artifacts[0].Name)
				assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(packageTestArchive)),
					artifacts[0].SHA256)
				assert.Equal(t, []string{checksumsPath}, requests)

				var archive bytes.Buffer

				require.NoError(t, source.Fetch(t.Context(), artifacts[0], &archive))
				assert.Equal(t, packageTestArchive, archive.Bytes())
				assert.Equal(t, []string{checksumsPath, downloadPath, "/asset"}, requests)
			})
		}
	}
}

func TestListWithoutBuilds(t *testing.T) {
	for _, test := range []struct{ os, arch, version string }{
		{goosDarwin, goarchARM64, "v2.15.0"}, {goosDarwin, goarchAMD64, "v3.0.0"},
		{goosLinux, "386", "v2.15.0"},
		{"freebsd", goarchAMD64, "v2.15.0"}, {goosLinux, goarchAMD64, "master"},
		{goosLinux, goarchAMD64, ""}, {goosLinux, goarchAMD64, "2.15"},
	} {
		t.Run(test.os+"/"+test.arch+"/"+test.version, func(t *testing.T) {
			client := packageTestClient(t, func(_ http.ResponseWriter,
				_ *http.Request,
			) {
				t.Error("unsupported platform or version must not make HTTP requests")
			})

			artifacts, err := packageTestSource(client).List(t.Context(),
				packageTestQuery(test.os, test.arch, test.version))
			require.NoError(t, err)
			require.Empty(t, artifacts)
		})
	}

	client := packageTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("development builds must not make HTTP requests")
	})
	query := packageTestQuery(goosLinux, goarchAMD64, "v2.15.0")

	query.Channel = bundle.ChannelDev

	artifacts, err := packageTestSource(client).List(t.Context(), query)
	require.NoError(t, err)
	require.Empty(t, artifacts)

	_, err = New().List(t.Context(), bundle.Query{
		Product: "tarantool", OS: goosLinux, Arch: goarchAMD64, Channel: bundle.ChannelRelease,
	})
	require.ErrorIs(t, err, errUnknownProduct)
}

func TestFetchErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		fileLimit int64
		chunked   bool
		want      error
	}{
		{name: "missing archive", status: 404, want: bundle.ErrNotFound},
		{name: "forbidden", status: 403, want: download.ErrHTTPStatus},
		{name: "rate limit", status: 429, want: download.ErrHTTPStatus},
		{name: "server error", status: 500, want: download.ErrHTTPStatus},
		{name: "asset limit", fileLimit: 12, want: download.ErrTooLarge},
		{name: "chunked asset limit", fileLimit: 12, chunked: true, want: download.ErrTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			const name = "tt-2.15.0-linux-amd64.tar.gz"

			client := packageTestClient(t, func(writer http.ResponseWriter,
				request *http.Request,
			) {
				if request.URL.Path == "/releases/download/v2.15.0/checksums.txt" {
					_, _ = writer.Write([]byte(packageTestChecksums(name, packageTestArchive)))

					return
				}

				assert.Equal(t, "/releases/download/v2.15.0/"+name, request.URL.Path)

				if test.status != 0 {
					writer.WriteHeader(test.status)

					return
				}

				if test.chunked {
					flusher, ok := writer.(http.Flusher)
					assert.True(t, ok)
					flusher.Flush()
				}

				_, _ = writer.Write(packageTestArchive)
			})

			source := packageTestSource(client)
			if test.fileLimit != 0 {
				source.client.FileLimit = test.fileLimit
			}

			artifacts, err := source.List(t.Context(),
				packageTestQuery(goosLinux, goarchAMD64, "v2.15.0"))
			require.NoError(t, err)
			require.Len(t, artifacts, 1)

			err = source.Fetch(t.Context(), artifacts[0], io.Discard)
			require.ErrorIs(t, err, test.want)

			if !errors.Is(test.want, bundle.ErrNotFound) {
				require.NotErrorIs(t, err, bundle.ErrNotFound,
					"a failed delivery must not fall back to a source build")
			}
		})
	}
}

func TestListChecksums(t *testing.T) {
	const name = "tt-2.15.0-linux-amd64.tar.gz"

	for _, test := range []struct {
		name      string
		status    int
		checksums string
		// sum is the digest listed, "-" for no artifact.
		sum  string
		want error
	}{
		{
			name:      "binary mode",
			checksums: fmt.Sprintf("%x *%s\n", sha256.Sum256(packageTestArchive), name),
			sum:       fmt.Sprintf("%x", sha256.Sum256(packageTestArchive)),
		},
		{name: "unpublished", status: http.StatusNotFound, sum: ""},
		{
			name:      "missing entry",
			checksums: packageTestChecksums("tt-2.15.0-darwin-amd64.tar.gz", packageTestArchive),
			sum:       "-",
		},
		{name: "malformed entry", checksums: "not-a-checksum  " + name + "\n",
			want: errPackageContents},
		{name: "server error", status: http.StatusInternalServerError,
			want: download.ErrHTTPStatus},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := packageTestClient(t, func(writer http.ResponseWriter,
				request *http.Request,
			) {
				switch request.URL.Path {
				case "/releases/download/v2.15.0/" + name:
					_, _ = writer.Write(packageTestArchive)
				case "/releases/download/v2.15.0/checksums.txt":
					if test.status != 0 {
						writer.WriteHeader(test.status)

						return
					}

					_, _ = writer.Write([]byte(test.checksums))
				default:
					t.Errorf("unexpected download request: %s", request.URL.Path)
					writer.WriteHeader(http.StatusNotFound)
				}
			})

			artifacts, err := packageTestSource(client).List(t.Context(),
				packageTestQuery(goosLinux, goarchAMD64, "v2.15.0"))

			if test.want != nil {
				require.ErrorIs(t, err, test.want)
				require.NotErrorIs(t, err, bundle.ErrNotFound)

				return
			}

			require.NoError(t, err)

			if test.sum == "-" {
				assert.Empty(t, artifacts, "an archive checksums.txt does not list is no build")

				return
			}

			require.Len(t, artifacts, 1)
			assert.Equal(t, test.sum, artifacts[0].SHA256)
		})
	}
}

func TestFetchUnverified(t *testing.T) {
	logger, recorder := logtest.New(t)
	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	client := packageTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/checksums.txt") {
			writer.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = writer.Write(packageTestArchive)
	})

	source := packageTestSource(client)
	artifacts, err := source.List(t.Context(), packageTestQuery(goosLinux, goarchAMD64, "v1.0.0"))
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Empty(t, recorder.Records(), "nothing is installed unverified yet")

	require.NoError(t, source.Fetch(t.Context(), artifacts[0], io.Discard))

	records := recorder.Records()
	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelWarn, records[0].Level)
	assert.Equal(t, "tt v1.0.0 publishes no checksums.txt; the archive is not verified",
		records[0].Message)
}

func TestLatest(t *testing.T) {
	for _, test := range []struct {
		name     string
		location string
		status   int
		want     string
	}{
		{name: "release", location: packageTestURL + "/releases/tag/v2.15.0", want: "v2.15.0"},
		{name: "relative redirect", location: "/tarantool/tt/releases/tag/v2.15.2",
			want: "v2.15.2"},
		{name: "no releases", location: packageTestURL + "/releases"},
		{name: "unexpected tag", location: packageTestURL + "/releases/tag/nightly"},
		{name: "no redirect", status: http.StatusOK},
		{name: "rate limit", status: http.StatusTooManyRequests},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests []string

			client := packageTestClient(t, func(writer http.ResponseWriter,
				request *http.Request,
			) {
				requests = append(requests, request.URL.Path)

				if test.location != "" {
					http.Redirect(writer, request, test.location, http.StatusFound)

					return
				}

				writer.WriteHeader(test.status)
			})

			tag, err := packageTestSource(client).Latest(t.Context())

			assert.Equal(t, []string{"/releases/latest"}, requests,
				"the redirect target must not be requested")

			if test.want == "" {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, tag)
		})
	}
}
