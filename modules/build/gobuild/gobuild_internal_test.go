package gobuild

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build/v3"
)

func TestGoArgs(t *testing.T) {
	t.Parallel()

	const output = "/proj/.rocks/bin/gw"

	tests := []struct {
		name   string
		config Config
		output string
		static bool
		jobs   int
		want   []string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args, err := goArgs(tt.config, tt.output, tt.static, tt.jobs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, args)
		})
	}

	_, err := goArgs(Config{Ldflags: []string{`both ' and "`}}, output, false, 0)
	require.ErrorIs(t, err, errUnquotableFlag)
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
