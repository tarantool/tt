package cargo

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

func TestArgs(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{
			"build", "--profile", "release", "--bin", "agent",
			"--message-format=json-render-diagnostics",
		},
		cargoBuild{bin: "agent"}.args(""))

	assert.Equal(t,
		[]string{
			"build", "--profile", "dist", "--bin", "agent",
			"--message-format=json-render-diagnostics",
			"--package", "agent-crate", "--features", "tls,serde/derive",
			"--target", "x86_64-unknown-linux-musl",
		},
		cargoBuild{config: Config{
			Profile: "dist", Package: "agent-crate", Features: []string{"tls", "serde/derive"},
		}, bin: "agent"}.args("x86_64-unknown-linux-musl"))
}

func TestModuleArgs(t *testing.T) {
	t.Parallel()

	fast := cargoBuild{config: Config{Package: "fast", Features: []string{"simd"}}, module: true}

	assert.Equal(t, []string{
		"rustc", "--lib", "--crate-type", "cdylib", "--profile", "release",
		"--message-format=json-render-diagnostics", "--package", "fast", "--features", "simd",
	}, fast.moduleArgs("", "linux"))

	// On macOS the library leaves its symbols of Tarantool to the loader, and
	// only the library: the arguments after -- go to its rustc alone.
	dev := cargoBuild{config: Config{Profile: "dev"}, module: true}

	assert.Equal(t, []string{
		"rustc", "--lib", "--crate-type", "cdylib", "--profile", "dev",
		"--message-format=json-render-diagnostics", "--target", "aarch64-apple-darwin",
		"--", "-C", "link-arg=-undefined", "-C", "link-arg=dynamic_lookup",
	}, dev.moduleArgs("aarch64-apple-darwin", darwinOS))
}

func TestTarget(t *testing.T) {
	t.Parallel()

	foreignArch := "arm64"
	if runtime.GOARCH == "arm64" {
		foreignArch = "amd64"
	}

	host := build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	plain := cargoBuild{bin: "agent"}
	static := cargoBuild{bin: "agent", static: true}
	staticModule := cargoBuild{module: true, static: true}

	tests := []struct {
		name     string
		build    cargoBuild
		platform build.Target
		want     string
	}{
		{"the host takes no target", plain, host, ""},
		{
			"static linux amd64", static, build.Target{OS: "linux", Arch: "amd64"},
			"x86_64-unknown-linux-musl",
		},
		{
			"static linux arm64", static, build.Target{OS: "linux", Arch: "arm64"},
			"aarch64-unknown-linux-musl",
		},
		// Tarantool, which loads a module, has the C library; the build only
		// checks the module needs no other.
		{"a static module of the host", staticModule, host, ""},
		{
			"a static module of another linux architecture", staticModule,
			build.Target{OS: "linux", Arch: foreignArch},
			cargoArches[foreignArch] + "-unknown-linux-gnu",
		},
		{
			"another linux architecture", plain, build.Target{OS: "linux", Arch: foreignArch},
			cargoArches[foreignArch] + "-unknown-linux-gnu",
		},
		{
			"another darwin architecture", plain, build.Target{OS: darwinOS, Arch: foreignArch},
			cargoArches[foreignArch] + "-apple-darwin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.platform == host && runtime.GOOS != "linux" && runtime.GOOS != darwinOS {
				t.Skip("cargo has no target for the host")
			}

			target, err := tt.build.target(tt.platform)
			require.NoError(t, err)
			assert.Equal(t, tt.want, target)
		})
	}

	_, err := plain.target(build.Target{OS: "linux", Arch: "riscv64"})
	require.ErrorIs(t, err, errUnsupportedTarget)

	_, err = plain.target(build.Target{OS: "windows", Arch: "amd64"})
	require.ErrorIs(t, err, errUnsupportedTarget)
}

func TestFindExecutable(t *testing.T) {
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

	path, err := findExecutable(stdout, "agent")
	require.NoError(t, err)
	assert.Equal(t, "/p/target/x86_64-unknown-linux-musl/release/agent", path)

	_, err = findExecutable(stdout, "missing")
	require.ErrorIs(t, err, errNoArtifact)
	assert.Contains(t, err.Error(), `for the binary "missing"`)
}

func TestFindLibrary(t *testing.T) {
	t.Parallel()

	stdout := []byte(`{"reason":"compiler-artifact","target":{"kind":["lib"],"name":"tlua"},` +
		`"filenames":["/p/target/release/deps/libtlua-1.rlib"]}
{"reason":"compiler-artifact","target":{"kind":["cdylib"],"name":"helper"},` +
		`"filenames":["/p/target/release/deps/libhelper.so"]}
a build script says hi
{"reason":"compiler-artifact","target":{"kind":["cdylib"],"name":"fast_rs"},` +
		`"filenames":["/p/target/release/libfast_rs.d","/p/target/release/libfast_rs.so"]}
{"reason":"build-finished","success":true}
`)

	library, name, err := findLibrary(stdout)
	require.NoError(t, err)
	assert.Equal(t, "/p/target/release/libfast_rs.so", library, "the package comes last")
	assert.Equal(t, "fast_rs", name)

	library, _, err = findLibrary([]byte(`{"reason":"compiler-artifact",` +
		`"target":{"kind":["cdylib"],"name":"m"},"filenames":["/p/libm.dylib"]}`))
	require.NoError(t, err)
	assert.Equal(t, "/p/libm.dylib", library)

	_, _, err = findLibrary([]byte(`{"reason":"build-finished","success":true}`))
	require.ErrorIs(t, err, errNoArtifact)
}

func TestProfile(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DefaultProfile, cargoBuild{}.profile())
	assert.Equal(t, "dev", cargoBuild{config: Config{Profile: "dev"}}.profile())
}
