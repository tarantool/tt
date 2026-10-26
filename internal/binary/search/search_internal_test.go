package search

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

type apiTestTransport func(*http.Request) (*http.Response, error)

func (transport apiTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestHTTPDoerAPIError(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		name := "complete body"
		if truncated {
			name = "truncated body"
		}

		t.Run(name, func(t *testing.T) {
			doer := NewTntIoDoer()

			doer.client = &http.Client{Transport: apiTestTransport(func(request *http.Request) (
				*http.Response, error,
			) {
				require.Equal(t, "api.test", request.URL.Host)
				require.Equal(t, http.MethodPost, request.Method)

				var body io.Reader = strings.NewReader("Invalid request data, usage:\n" +
					`{"username": username, "password": password, "query": query}.`)

				if truncated {
					body = io.MultiReader(body, iotest.ErrReader(io.ErrUnexpectedEOF))
				}

				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Status:     "400 Bad Request",
					Body:       io.NopCloser(body),
					Request:    request,
				}, nil
			})}

			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				"https://api.test", nil)
			require.NoError(t, err)

			_, err = doer.Do(request)
			require.ErrorContains(t, err, "API request failed with status 400 Bad Request")
			require.ErrorContains(t, err, "Invalid request data, usage:")
			require.NotContains(t, err.Error(), "%!w")

			if truncated {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			}
		})
	}
}

func Test_getBundles(t *testing.T) {
	pkgRelease := "tarantool-enterprise-sdk-nogc64-3.4.1-0-r63.linux.x86_64.tar.gz"
	pkgDebug := "tarantool-enterprise-sdk-debug-nogc64-3.4.1-0-r63.linux.x86_64.tar.gz"
	pkgOld := "tarantool-enterprise-sdk-gc64-2.11.1-0-r563.linux.x86_64.tar.gz"

	type args struct {
		rawBundleInfoList map[string][]string
		flags             SearchFlags
	}

	tests := []struct {
		name    string
		args    args
		want    BundleInfoSlice
		wantErr bool
	}{
		{
			name: "Random data",
			args: args{
				map[string][]string{"random": {"data"}},
				SearchRelease,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Search release (OK)",
			args: args{
				map[string][]string{"3.4": {pkgRelease}},
				SearchRelease,
			},
			want: BundleInfoSlice{
				{
					Version: version.Version{
						Major:      3,
						Minor:      4,
						Patch:      1,
						Additional: 0,
						Revision:   63,
						Release:    version.Release{Type: version.TypeRelease},
						Hash:       "",
						Str:        "nogc64-3.4.1-0-r63",
						Tarball:    pkgRelease,
						BuildName:  "nogc64",
					},
					Release: "3.4",
					Package: "enterprise",
				},
			},
			wantErr: false,
		},
		{
			name: "Search debug (OK)",
			args: args{
				map[string][]string{"3.4": {pkgDebug}},
				SearchDebug,
			},
			want: BundleInfoSlice{
				{
					Version: version.Version{
						Major:      3,
						Minor:      4,
						Patch:      1,
						Additional: 0,
						Revision:   63,
						Release:    version.Release{Type: version.TypeRelease},
						Hash:       "",
						Str:        "debug-nogc64-3.4.1-0-r63",
						Tarball:    pkgDebug,
						BuildName:  "debug-nogc64",
					},
					Release: "3.4",
					Package: "enterprise",
				},
			},
			wantErr: false,
		},
		{
			name: "Tarantool 2.x is not offered",
			args: args{
				map[string][]string{"2.11": {pkgOld}, "3.4": {pkgRelease}},
				SearchRelease,
			},
			want: BundleInfoSlice{
				{
					Version: version.Version{
						Major:      3,
						Minor:      4,
						Patch:      1,
						Additional: 0,
						Revision:   63,
						Release:    version.Release{Type: version.TypeRelease},
						Hash:       "",
						Str:        "nogc64-3.4.1-0-r63",
						Tarball:    pkgRelease,
						BuildName:  "nogc64",
					},
					Release: "3.4",
					Package: "enterprise",
				},
			},
			wantErr: false,
		},
		{
			name: "Only Tarantool 2.x (Err)",
			args: args{
				map[string][]string{"2.11": {pkgOld}},
				SearchRelease,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Search release (Err)",
			args: args{
				map[string][]string{"3.4": {pkgDebug}},
				SearchRelease,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Search debug (Err)",
			args: args{
				map[string][]string{"3.4": {pkgRelease}},
				SearchDebug,
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sCtx := SearchCtx{
				Program: binary.ProgramEe,
				Package: "enterprise",
				Filter:  tt.args.flags,
			}

			got, err := getBundles(tt.args.rawBundleInfoList, &sCtx)

			if (err != nil) != tt.wantErr {
				t.Errorf("getBundles() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			assert.Equalf(t, tt.want, got, "getBundles()")
		})
	}
}

func TestIsMasked(t *testing.T) {
	old, err := version.Parse("gc64-2.11.1-0-r563")
	require.NoError(t, err)

	current, err := version.Parse("gc64-3.0.0-0-r1")
	require.NoError(t, err)

	for _, program := range []binary.Program{
		binary.ProgramCe, binary.ProgramEe, binary.ProgramSDK,
	} {
		assert.True(t, isMasked(program, old), program.String())
		assert.False(t, isMasked(program, current), program.String())
	}

	// tt, tt-ee and TCM have versions of their own.
	for _, program := range []binary.Program{
		binary.ProgramTt, binary.ProgramTtEe, binary.ProgramTcm,
	} {
		assert.False(t, isMasked(program, old), program.String())
	}
}

func TestNewSearchCtx(t *testing.T) {
	t.Run("Valid context", func(t *testing.T) {
		got := NewSearchCtx(NewPlatformInformer(), NewTntIoDoer())
		require.NotNil(t, got)
		assert.Equal(t, SearchRelease, got.Filter)
		assert.Equal(t, binary.ProgramUnknown, got.Program)
		assert.Empty(t, got.Package)
		assert.Empty(t, got.ReleaseVersion)
		assert.False(t, got.DevBuilds)
		assert.Implements(t, (*PlatformInformer)(nil), got.platformInformer)
		assert.Implements(t, (*binary.TntIoDoer)(nil), got.TntIoDoer)
	})
}
