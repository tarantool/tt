package gobuild

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

func TestGoArgs(t *testing.T) {
	t.Parallel()

	const output = "/proj/.rocks/bin/gw"

	tests := []struct {
		name    string
		config  Config
		output  string
		static  bool
		jobs    int
		options []string
		want    []string
	}{
		{
			name:   "defaults",
			config: Config{},
			output: output,
			want:   []string{"build", "-trimpath", "-o", output, "."},
		},
		{
			name:   "a package",
			config: Config{Package: "./cmd/gw"},
			output: "/proj/.rocks/bin/gateway",
			want:   []string{"build", "-trimpath", "-o", "/proj/.rocks/bin/gateway", "./cmd/gw"},
		},
		{
			// Every entry is one argument of the linker.
			name: "tags and ldflags",
			config: Config{
				Tags:    []string{"prod", "fast"},
				Ldflags: []string{"-s", "-X", "main.greeting=hello world"},
			},
			output: output,
			want: []string{
				"build", "-trimpath", "-o", output,
				"-tags", "prod,fast", "-ldflags", "-s -X 'main.greeting=hello world'", ".",
			},
		},
		{
			// Without cgo a Go program is static already.
			name:   "static without cgo",
			config: Config{},
			output: output,
			static: true,
			want:   []string{"build", "-trimpath", "-o", output, "."},
		},
		{
			name: "static with cgo",
			config: Config{
				Cgo: true, Tags: []string{"prod"}, Ldflags: []string{"-s"},
			},
			output: output,
			static: true,
			want: []string{
				"build", "-trimpath", "-o", output,
				"-tags", "netgo,osusergo,prod",
				"-ldflags", "-linkmode external -extldflags -static -s", ".",
			},
		},
		{
			// cgo alone links dynamically: no static flags.
			name:   "cgo without static",
			config: Config{Cgo: true},
			output: output,
			want:   []string{"build", "-trimpath", "-o", output, "."},
		},
		{
			// The jobs of the build, when they are set.
			name:   "jobs",
			config: Config{Tags: []string{"prod"}},
			output: output,
			jobs:   3,
			want: []string{
				"build", "-trimpath", "-o", output, "-p", "3", "-tags", "prod", ".",
			},
		},
		{
			// The options of a build from a mirror, before the package.
			name:    "options",
			config:  Config{Package: "./cmd/gw", Tags: []string{"prod"}},
			output:  output,
			options: []string{"-mod=readonly"},
			want: []string{
				"build", "-trimpath", "-o", output, "-tags", "prod", "-mod=readonly", "./cmd/gw",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args, err := goArgs(tt.config, tt.output, tt.static, tt.jobs, tt.options)
			require.NoError(t, err)
			assert.Equal(t, tt.want, args)
		})
	}

	_, err := goArgs(Config{Ldflags: []string{`both ' and "`}}, output, false, 0, nil)
	require.ErrorIs(t, err, errUnquotableFlag)
}

// TestMirrorEnv: a build from a mirror has the mirror for its only module
// proxy, no checksum database and no private module.
func TestMirrorEnv(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{
		"GOPROXY=file:///srv/mirror/go", "GOSUMDB=off", "GONOPROXY=,",
	}, mirrorEnv("/srv/mirror/go"))
}

// TestFileURL: every element of the path is escaped, a comma and a bar,
// which separate the proxies of GOPROXY, among them.
func TestFileURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "file:///srv/mirror/go", fileURL("/srv/mirror/go"))
	assert.Equal(t, "file:///srv/a%20b%2Cc%7Cd/%23e%25f", fileURL("/srv/a b,c|d/#e%f"))
	assert.Equal(t, "file:///srv/mirror/go", fileURL("/srv/mirror/../mirror/go/"))
}

// TestDownloadEnv: go mod download of Vendor fills a module cache of its own,
// and takes the modules from the module cache of the user before its GOPROXY.
func TestDownloadEnv(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{
		"GOMODCACHE=/tmp/vendor/cache",
		"GOPROXY=file:///home/u/go/pkg/mod/cache/download,https://proxy.golang.org,direct",
	}, downloadEnv(goModule{
		goMod: "/p/go.mod", goWork: "", modCache: "/home/u/go/pkg/mod",
		proxy: "https://proxy.golang.org,direct",
	}, "/tmp/vendor/cache"))

	// GOPROXY=off takes what the user downloaded alone.
	assert.Equal(t, []string{
		"GOMODCACHE=/tmp/vendor/cache", "GOPROXY=file:///home/u/go/pkg/mod/cache/download,off",
	}, downloadEnv(goModule{
		goMod: "/p/go.mod", goWork: "", modCache: "/home/u/go/pkg/mod", proxy: "off",
	}, "/tmp/vendor/cache"))
}

func TestQuoteFlags(t *testing.T) {
	t.Parallel()

	quoted, err := QuoteFlags([]string{"-s", "-X", "main.v=a b", `it's`, `say "hi" now`})
	require.NoError(t, err)
	assert.Equal(t, `-s -X 'main.v=a b' "it's" 'say "hi" now'`, quoted)

	_, err = QuoteFlags([]string{`both ' and "`})
	require.ErrorIs(t, err, errUnquotableFlag)
}

func TestTargetEnv(t *testing.T) {
	t.Parallel()

	target := build.Target{OS: "linux", Arch: "arm64"}

	assert.Equal(t, []string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0"},
		targetEnv(Config{}, target))
	assert.Equal(t, []string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=1"},
		targetEnv(Config{Cgo: true}, target))
}
