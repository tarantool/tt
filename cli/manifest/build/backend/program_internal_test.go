package backend

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// on returns a pointer to true, for the *bool fields of a build.
func on() *bool {
	value := true

	return &value
}

func TestGoArgs(t *testing.T) {
	t.Parallel()

	env := Env{OutputDir: "/proj/.rocks/bin", Component: "gw"}

	tests := []struct {
		name  string
		build manifest.Build
		want  []string
	}{
		{
			name:  "defaults",
			build: manifest.Build{Backend: BackendGo},
			want:  []string{"build", "-trimpath", "-o", "/proj/.rocks/bin/gw", "."},
		},
		{
			name:  "name and package",
			build: manifest.Build{Backend: BackendGo, Name: "gateway", Package: "./cmd/gw"},
			want:  []string{"build", "-trimpath", "-o", "/proj/.rocks/bin/gateway", "./cmd/gw"},
		},
		{
			// Every entry is one argument of the linker.
			name: "tags and ldflags",
			build: manifest.Build{
				Backend: BackendGo,
				Tags:    []string{"prod", "fast"},
				Ldflags: []string{"-s", "-X", "main.greeting=hello world"},
			},
			want: []string{
				"build", "-trimpath", "-o", "/proj/.rocks/bin/gw",
				"-tags", "prod,fast", "-ldflags", "-s -X 'main.greeting=hello world'", ".",
			},
		},
		{
			// Without cgo a Go program is static already.
			name:  "static without cgo",
			build: manifest.Build{Backend: BackendGo, Static: on()},
			want:  []string{"build", "-trimpath", "-o", "/proj/.rocks/bin/gw", "."},
		},
		{
			name: "static with cgo",
			build: manifest.Build{
				Backend: BackendGo, Static: on(), Cgo: on(),
				Tags: []string{"prod"}, Ldflags: []string{"-s"},
			},
			want: []string{
				"build", "-trimpath", "-o", "/proj/.rocks/bin/gw",
				"-tags", "netgo,osusergo,prod",
				"-ldflags", "-linkmode external -extldflags -static -s", ".",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args, err := goArgs(tt.build, env)
			require.NoError(t, err)
			assert.Equal(t, tt.want, args)
		})
	}
}

func TestQuoteFlags(t *testing.T) {
	t.Parallel()

	quoted, err := QuoteFlags([]string{"-s", "-X", "main.v=a b", `it's`, `say "hi" now`})
	require.NoError(t, err)
	assert.Equal(t, `-s -X 'main.v=a b' "it's" 'say "hi" now'`, quoted)

	_, err = QuoteFlags([]string{`both ' and "`})
	require.ErrorIs(t, err, errUnquotableFlag)
}

// TestGoEnvironSelectsTheTarget: the build platform and cgo are set after
// [build].env, so the manifest's env cannot build for another platform than
// the one the program is checked against.
func TestGoEnvironSelectsTheTarget(t *testing.T) {
	t.Parallel()

	env := Env{
		OS: "linux", Arch: "arm64",
		Extra: map[string]string{"GOOS": "darwin", "CGO_ENABLED": "1", "CC": "musl-gcc"},
	}

	got := goEnviron(manifest.Build{Backend: BackendGo}, env)
	assert.Equal(t, "linux", lastEnvValue(got, "GOOS"))
	assert.Equal(t, "arm64", lastEnvValue(got, "GOARCH"))
	assert.Equal(t, "0", lastEnvValue(got, "CGO_ENABLED"))
	assert.Equal(t, "musl-gcc", lastEnvValue(got, "CC"))

	got = goEnviron(manifest.Build{Backend: BackendGo, Cgo: on()}, env)
	assert.Equal(t, "1", lastEnvValue(got, "CGO_ENABLED"))
}

func TestCargoArgs(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{
			"build", "--profile", "release", "--bin", "agent",
			"--message-format=json-render-diagnostics",
		},
		cargoArgs(manifest.Build{Backend: BackendCargo}, "agent", ""))

	assert.Equal(t,
		[]string{
			"build", "--profile", "dist", "--bin", "agent",
			"--message-format=json-render-diagnostics",
			"--package", "agent-crate", "--features", "tls,serde/derive",
			"--target", "x86_64-unknown-linux-musl",
		},
		cargoArgs(manifest.Build{
			Backend: BackendCargo, Profile: "dist", Package: "agent-crate",
			Features: []string{"tls", "serde/derive"},
		}, "agent", "x86_64-unknown-linux-musl"))
}

func TestCargoTarget(t *testing.T) {
	t.Parallel()

	foreignArch := "arm64"
	if runtime.GOARCH == "arm64" {
		foreignArch = "amd64"
	}

	plain := manifest.Build{Backend: BackendCargo}
	static := manifest.Build{Backend: BackendCargo, Static: on()}

	tests := []struct {
		name  string
		build manifest.Build
		env   Env
		want  string
	}{
		{"the host takes no target", plain, Env{}, ""},
		{
			"static linux amd64",
			static,
			Env{OS: "linux", Arch: "amd64"},
			"x86_64-unknown-linux-musl",
		},
		{
			"static linux arm64",
			static,
			Env{OS: "linux", Arch: "arm64"},
			"aarch64-unknown-linux-musl",
		},
		{
			"another linux architecture",
			plain,
			Env{OS: "linux", Arch: foreignArch},
			cargoArches[foreignArch] + "-unknown-linux-gnu",
		},
		{
			"another darwin architecture",
			plain,
			Env{OS: darwinOS, Arch: foreignArch},
			cargoArches[foreignArch] + "-apple-darwin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target, err := cargoTarget(tt.build, tt.env)
			require.NoError(t, err)
			assert.Equal(t, tt.want, target)
		})
	}

	_, err := cargoTarget(plain, Env{OS: "linux", Arch: "riscv64"})
	require.ErrorIs(t, err, errUnsupportedTarget)

	_, err = cargoTarget(plain, Env{OS: "windows", Arch: "amd64"})
	require.ErrorIs(t, err, errUnsupportedTarget)
}

func TestCargoExecutable(t *testing.T) {
	t.Parallel()

	stdout := []byte(`{"reason":"compiler-artifact","target":{"kind":["lib"],"name":"agent"},` +
		`"executable":null}
build script says hi
{"reason":"compiler-artifact","target":{"kind":["bin"],"name":"helper"},` +
		`"executable":"/p/target/release/helper"}
{"reason":"compiler-artifact","target":{"kind":["bin"],"name":"agent"},` +
		`"executable":"/p/target/x86_64-unknown-linux-musl/release/agent"}
{"reason":"build-finished","success":true}
`)

	path, err := cargoExecutable(stdout, "agent")
	require.NoError(t, err)
	assert.Equal(t, "/p/target/x86_64-unknown-linux-musl/release/agent", path)

	_, err = cargoExecutable(stdout, "missing")
	require.ErrorIs(t, err, errNoArtifact)
}

func TestPreflight(t *testing.T) {
	t.Parallel()

	t.Run("static for darwin", func(t *testing.T) {
		t.Parallel()

		for _, backend := range []string{BackendGo, BackendCargo} {
			err := Preflight(manifest.Build{Backend: backend, Static: on()},
				Env{OS: darwinOS, Arch: "arm64"})
			require.ErrorIs(t, err, ErrStaticDarwin, backend)
		}
	})

	t.Run("a platform cargo has no target for", func(t *testing.T) {
		t.Parallel()

		err := Preflight(manifest.Build{Backend: BackendCargo}, Env{OS: "linux", Arch: "s390x"})
		require.ErrorIs(t, err, errUnsupportedTarget)
	})

	t.Run("other backends pass", func(t *testing.T) {
		t.Parallel()

		err := Preflight(manifest.Build{Backend: BackendMake, Static: on()},
			Env{OS: darwinOS, Arch: "arm64"})
		require.NoError(t, err)
	})
}

// TestPreflightNamesTheMissingTool cannot run in parallel: it empties PATH.
func TestPreflightNamesTheMissingTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	for backend, tool := range map[string]string{BackendGo: "go", BackendCargo: "cargo"} {
		err := Preflight(manifest.Build{Backend: backend}, Env{})
		require.ErrorIs(t, err, ErrNoTool, backend)
		assert.Contains(t, err.Error(), `runs "`+tool+`", which is not in PATH`)
	}
}
