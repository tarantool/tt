package rocks_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/rocks"
)

func TestEffectiveRegistriesPrecedence(t *testing.T) {
	t.Parallel()

	const (
		flagURL     = "https://flag.example/"
		envURL      = "https://env.example/"
		manifestURL = "https://manifest.example/"
	)

	full := rocks.Sources{
		Flag:       []string{flagURL},
		Env:        []string{envURL},
		Manifest:   []string{manifestURL},
		WorkingDir: "/cwd",
		ProjectDir: "/project",
	}

	cases := []struct {
		name    string
		mutate  func(*rocks.Sources)
		wantURL string
		want    rocks.Source
	}{
		{
			name:    "flag wins over everything",
			mutate:  func(_ *rocks.Sources) {},
			wantURL: flagURL,
			want:    rocks.SourceFlag,
		},
		{
			name:    "env wins without a flag",
			mutate:  func(s *rocks.Sources) { s.Flag = nil },
			wantURL: envURL,
			want:    rocks.SourceEnv,
		},
		{
			name:    "manifest wins without a flag or env",
			mutate:  func(s *rocks.Sources) { s.Flag, s.Env = nil, nil },
			wantURL: manifestURL,
			want:    rocks.SourceManifest,
		},
		{
			name:    "defaults when nothing is configured",
			mutate:  func(s *rocks.Sources) { s.Flag, s.Env, s.Manifest = nil, nil, nil },
			wantURL: rocks.ServerTarantool,
			want:    rocks.SourceDefault,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			sources := full
			testCase.mutate(&sources)

			effective, err := rocks.EffectiveRegistries(sources)
			require.NoError(t, err)
			require.NotEmpty(t, effective)

			assert.Equal(t, testCase.wantURL, effective[0].URL)
			assert.Equal(t, testCase.want, effective[0].Source)
		})
	}
}

func TestEffectiveRegistriesLayersDoNotMerge(t *testing.T) {
	t.Parallel()

	// A rock-server list is ordered and first-found-wins, so the layer that
	// wins replaces the ones below it instead of being prepended to them.
	effective, err := rocks.EffectiveRegistries(rocks.Sources{
		Flag:       []string{"https://a.example/", "https://b.example/"},
		Env:        []string{"https://env.example/"},
		Manifest:   []string{"https://manifest.example/"},
		WorkingDir: "/cwd",
		ProjectDir: "/project",
	})
	require.NoError(t, err)

	assert.Equal(t, []rocks.Registry{
		{URL: "https://a.example/", Source: rocks.SourceFlag},
		{URL: "https://b.example/", Source: rocks.SourceFlag},
	}, effective)
}

func TestEffectiveRegistriesDefaultsAreTheServerList(t *testing.T) {
	t.Parallel()

	effective, err := rocks.EffectiveRegistries(rocks.Sources{})
	require.NoError(t, err)

	assert.Equal(t, rocks.DefaultServers(), rocks.URLs(effective))
}

func TestEffectiveRegistriesResolvesRelativePaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		sources rocks.Sources
		want    string
	}{
		{
			name: "a flag path is relative to the working directory",
			sources: rocks.Sources{
				Flag:       []string{"./mirror"},
				WorkingDir: "/cwd",
				ProjectDir: "/project",
			},
			want: filepath.Join("/cwd", "mirror"),
		},
		{
			name: "an env path is relative to the working directory",
			sources: rocks.Sources{
				Env:        []string{"mirror"},
				WorkingDir: "/cwd",
				ProjectDir: "/project",
			},
			want: filepath.Join("/cwd", "mirror"),
		},
		{
			name: "a manifest path is relative to the project directory",
			sources: rocks.Sources{
				Manifest:   []string{"./mirror"},
				WorkingDir: "/cwd",
				ProjectDir: "/project",
			},
			want: filepath.Join("/project", "mirror"),
		},
		{
			name: "an absolute path is kept",
			sources: rocks.Sources{
				Flag:       []string{"/srv/mirror"},
				WorkingDir: "/cwd",
			},
			want: "/srv/mirror",
		},
		{
			name: "a file URL becomes a bare path",
			sources: rocks.Sources{
				Flag:       []string{"file:///srv/mirror"},
				WorkingDir: "/cwd",
			},
			want: "/srv/mirror",
		},
		{
			name: "a relative file URL resolves too",
			sources: rocks.Sources{
				Flag:       []string{"file://mirror"},
				WorkingDir: "/cwd",
			},
			want: filepath.Join("/cwd", "mirror"),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			effective, err := rocks.EffectiveRegistries(testCase.sources)
			require.NoError(t, err)
			require.Len(t, effective, 1)

			assert.Equal(t, testCase.want, effective[0].URL)
		})
	}
}

func TestEffectiveRegistriesLeavesURLsAlone(t *testing.T) {
	t.Parallel()

	// An https server has a host, not a path: making it "absolute" against a
	// directory would turn it into a directory that does not exist.
	effective, err := rocks.EffectiveRegistries(rocks.Sources{
		Flag:       []string{"https://rocks.example/dist/"},
		WorkingDir: "/cwd",
	})
	require.NoError(t, err)

	assert.Equal(t, "https://rocks.example/dist/", effective[0].URL)
}

func TestEffectiveRegistriesRejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		sources rocks.Sources
		wantErr error
	}{
		{
			name:    "an empty entry",
			sources: rocks.Sources{Flag: []string{"https://a.example/", "  "}},
			wantErr: rocks.ErrEmptyRegistry,
		},
		{
			name: "the same server twice",
			sources: rocks.Sources{
				Flag: []string{"https://a.example/", "https://a.example/"},
			},
			wantErr: rocks.ErrDuplicateRegistry,
		},
		{
			name:    "a relative path with no base directory",
			sources: rocks.Sources{Flag: []string{"./mirror"}},
			wantErr: rocks.ErrNoBaseDir,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := rocks.EffectiveRegistries(testCase.sources)
			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestParseRegistryList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "blank", raw: "   ", want: nil},
		{name: "one", raw: "https://a.example/", want: []string{"https://a.example/"}},
		{
			name: "several, whitespace trimmed",
			raw:  " https://a.example/ , ./mirror ",
			want: []string{"https://a.example/", "./mirror"},
		},
		{
			// Kept rather than dropped, so EffectiveRegistries reports the
			// stray comma instead of quietly resolving a shorter list.
			name: "a blank entry survives to be reported",
			raw:  "https://a.example/,,./mirror",
			want: []string{"https://a.example/", "", "./mirror"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, rocks.ParseRegistryList(testCase.raw))
		})
	}
}
