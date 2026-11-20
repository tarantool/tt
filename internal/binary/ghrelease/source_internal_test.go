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
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/v3/bundle"
	"github.com/tarantool/tt/sdk/v3/log/logtest"
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
		apiURL:      packageTestURL + "/api/releases",
		major:       3,
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

func TestLatestRedirect(t *testing.T) {
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

			tag, err := packageTestSource(client).latestRedirect(t.Context())

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

// packageTestReleases is a page of the API listing of releases: stable
// ones, "-pre" ones, "-draft" ones.
func packageTestReleases(tags ...string) string {
	releases := make([]string, 0, len(tags))

	for _, tag := range tags {
		name, kind, _ := strings.Cut(tag, "/")

		releases = append(releases, fmt.Sprintf(
			`{"tag_name": %q, "draft": %t, "prerelease": %t, "assets": []}`,
			name, kind == "draft", kind == "pre"))
	}

	return "[" + strings.Join(releases, ",") + "]"
}

func TestLatest(t *testing.T) {
	// A page full of the releases of tt 2, the API listing being newest first.
	fullPage := make([]string, 0, releasesPerPage)
	for patch := range releasesPerPage - 1 {
		fullPage = append(fullPage, fmt.Sprintf("v2.%d.0", 200-patch))
	}

	fullPage = append(fullPage, "v3.1.0-alpha.1/pre")

	for _, test := range []struct {
		name string
		// latest is the tag the page of the latest release redirects to,
		// none for a failure.
		latest string
		// pages are the pages of the listing; nil for a listing that fails.
		pages  [][]string
		status int
		want   string
		// listed is whether the listing is asked.
		listed  bool
		wantErr error
	}{
		{name: "the latest release is of tt 3", latest: "v3.1.0", want: "v3.1.0"},
		{
			name: "tt 3 has pre-releases only", latest: "v2.15.2", listed: true,
			pages: [][]string{{
				"v2.15.2", "v3.0.0-alpha.3/pre", "v2.15.1", "v3.0.0-alpha.2/pre",
			}},
			want: "v3.0.0-alpha.3",
		},
		{
			name: "a stable tt 3 beats a newer pre-release", latest: "v2.16.0", listed: true,
			pages: [][]string{{
				"v2.16.0", "v3.1.0-rc1/pre", "v3.0.0", "v3.0.0-alpha.3/pre",
			}},
			want: "v3.0.0",
		},
		{
			name: "drafts and other tags are skipped", latest: "v2.15.2", listed: true,
			pages: [][]string{{"v3.1.0/draft", "nightly", "v3.0.0-alpha.1/pre", "v4.0.0"}},
			want:  "v3.0.0-alpha.1",
		},
		{
			name: "a stable release behind a full page", latest: "v2.15.2", listed: true,
			pages: [][]string{fullPage, {"v3.0.0", "v2.0.0"}}, want: "v3.0.0",
		},
		{
			name: "a pre-release on a full page", latest: "v2.15.2", listed: true,
			pages: [][]string{fullPage, {"v2.0.0"}}, want: "v3.1.0-alpha.1",
		},
		{
			name: "the redirect fails", listed: true,
			pages: [][]string{{"v3.0.0-alpha.2/pre"}}, want: "v3.0.0-alpha.2",
		},
		{
			name: "no release of tt 3", latest: "v2.15.2", listed: true,
			pages: [][]string{{"v2.15.2", "v2.15.1"}}, wantErr: errNoRelease,
		},
		{
			name: "rate limit", latest: "v2.15.2", listed: true, status: http.StatusForbidden,
			wantErr: download.ErrHTTPStatus,
		},
		{
			name: "malformed listing", latest: "v2.15.2", listed: true,
			pages: [][]string{{"{"}}, wantErr: errPackageContents,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(tokenVariable, "")

			listed := false

			client := packageTestClient(t, func(writer http.ResponseWriter,
				request *http.Request,
			) {
				switch request.URL.Path {
				case "/releases/latest":
					if test.latest == "" {
						writer.WriteHeader(http.StatusInternalServerError)

						return
					}

					http.Redirect(writer, request, "/releases/tag/"+test.latest, http.StatusFound)
				case "/api/releases":
					listed = true

					assert.Equal(t, "application/vnd.github+json", request.Header.Get("Accept"))
					assert.Empty(t, request.Header.Get("Authorization"))
					assert.Equal(t, "100", request.URL.Query().Get("per_page"))

					if test.status != 0 {
						writer.WriteHeader(test.status)

						return
					}

					page, err := strconv.Atoi(request.URL.Query().Get("page"))
					if !assert.NoError(t, err) || page > len(test.pages) {
						_, _ = writer.Write([]byte("[]"))

						return
					}

					body := packageTestReleases(test.pages[page-1]...)
					if test.pages[page-1][0] == "{" {
						body = "{"
					}

					_, _ = writer.Write([]byte(body))
				default:
					t.Errorf("unexpected request: %s", request.URL.Path)
					writer.WriteHeader(http.StatusNotFound)
				}
			})

			tag, err := packageTestSource(client).Latest(t.Context())

			assert.Equal(t, test.listed, listed)

			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)

				if test.status == http.StatusForbidden {
					require.ErrorContains(t, err, tokenVariable)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, tag)
		})
	}
}

func TestLatestToken(t *testing.T) {
	t.Setenv(tokenVariable, "secret")

	client := packageTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/releases/latest" {
			http.Redirect(writer, request, "/releases/tag/v2.15.2", http.StatusFound)

			return
		}

		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))

		_, _ = writer.Write([]byte(packageTestReleases("v3.0.0-alpha.2/pre")))
	})

	tag, err := packageTestSource(client).Latest(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "v3.0.0-alpha.2", tag)
}

// TestReleases lists the releases of every major version of tt, stable ones
// and pre-releases, newest first, page after page, without drafts and tags
// that name no version.
func TestReleases(t *testing.T) {
	fullPage := make([]string, 0, releasesPerPage)
	for minor := range releasesPerPage {
		fullPage = append(fullPage, fmt.Sprintf("v2.%d.0", 200-minor))
	}

	for _, test := range []struct {
		name string
		// pages are the pages of the listing; nil for a listing that fails.
		pages   [][]string
		status  int
		want    []string
		wantErr error
		// asked is the number of pages asked.
		asked int
	}{
		{
			name: "every major version",
			pages: [][]string{{
				"v3.1.0/draft", "v3.0.0-alpha.2/pre", "nightly", "v2.15.2", "2.0.0",
			}},
			want: []string{"v3.0.0-alpha.2", "v2.15.2", "2.0.0"}, asked: 1,
		},
		{
			name: "a page after a full one", pages: [][]string{fullPage, {"v1.0.0"}},
			want: append(slices.Clone(fullPage), "v1.0.0"), asked: 2,
		},
		{name: "no releases", pages: [][]string{{}}, asked: 1},
		{
			name: "rate limit", status: http.StatusForbidden, wantErr: download.ErrHTTPStatus,
			asked: 1,
		},
		{
			name: "malformed listing", pages: [][]string{{"{"}}, wantErr: errPackageContents,
			asked: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(tokenVariable, "")

			asked := 0

			client := packageTestClient(t, func(writer http.ResponseWriter,
				request *http.Request,
			) {
				if !assert.Equal(t, "/api/releases", request.URL.Path) {
					writer.WriteHeader(http.StatusNotFound)

					return
				}

				asked++

				if test.status != 0 {
					writer.WriteHeader(test.status)

					return
				}

				page, err := strconv.Atoi(request.URL.Query().Get("page"))
				if !assert.NoError(t, err) || page > len(test.pages) {
					_, _ = writer.Write([]byte("[]"))

					return
				}

				body := packageTestReleases(test.pages[page-1]...)
				if len(test.pages[page-1]) > 0 && test.pages[page-1][0] == "{" {
					body = "{"
				}

				_, _ = writer.Write([]byte(body))
			})

			tags, err := packageTestSource(client).Releases(t.Context())

			assert.Equal(t, test.asked, asked)

			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)

				if test.status == http.StatusForbidden {
					require.ErrorContains(t, err, tokenVariable)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, tags)
		})
	}
}

func TestCheckPlatform(t *testing.T) {
	source := New()

	for _, test := range []struct {
		tag, os, arch string
		// want is the platforms the error names, none for a supported one.
		want string
	}{
		{tag: "v3.0.0", os: goosLinux, arch: goarchAMD64},
		{tag: "v3.0.0", os: goosLinux, arch: goarchARM64},
		{tag: "v3.0.0-alpha.2", os: goosDarwin, arch: goarchARM64},
		{tag: "v2.15.0", os: goosDarwin, arch: goarchAMD64},
		{
			tag: "v3.0.0", os: goosDarwin, arch: goarchAMD64,
			want: "only for linux/amd64, linux/arm64, darwin/arm64",
		},
		{
			tag: "v2.15.0", os: goosDarwin, arch: goarchARM64,
			want: "only for linux/amd64, linux/arm64, darwin/amd64",
		},
		{
			tag: "v3.0.0", os: goosLinux, arch: "386",
			want: "only for linux/amd64, linux/arm64, darwin/arm64",
		},
	} {
		t.Run(test.tag+"/"+test.os+"/"+test.arch, func(t *testing.T) {
			err := source.CheckPlatform(test.tag, test.os, test.arch)
			if test.want == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, errUnsupportedPlatform)
			require.ErrorContains(t, err, fmt.Sprintf("no prebuilt tt %s for %s/%s, %s",
				test.tag, test.os, test.arch, test.want))
		})
	}
}

func TestTagMajor(t *testing.T) {
	for tag, want := range map[string]uint64{
		"v3.0.0": 3, "3.0.0-alpha.2": 3, "v2.15.2": 2, "v10.1.0": 10, "nightly": 0, "": 0,
	} {
		assert.Equal(t, want, tagMajor(tag), tag)
	}
}
