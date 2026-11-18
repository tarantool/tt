package pack

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// wasmComponent is the preamble of a WebAssembly component, which is
// neither ELF nor Mach-O.
const wasmComponent = "\x00asm\x0d\x00\x01\x00"

// TestRockArch checks that only native files make the rock of a stage the
// rock of the host: an ELF or Mach-O file anywhere, or a file of lib/ named
// as a native module is, whatever its content; a .wasm or Lua in lib/, and
// data files, make the rock run anywhere, as the .tt archive of the same
// files does.
func TestRockArch(t *testing.T) {
	t.Parallel()

	host, err := os.Executable()
	require.NoError(t, err)

	program, err := os.ReadFile(host)
	require.NoError(t, err)

	for name, test := range map[string]struct {
		files map[string]string
		arch  string
	}{
		"lua": {files: map[string]string{"lua/app/init.lua": "return {}\n"}, arch: rockArchAll},
		"wasm and lua in lib": {
			files: map[string]string{
				"lib/app/component.wasm": wasmComponent,
				"lib/app/glue.lua":       "return {}\n",
				"lua/app/init.lua":       "return {}\n",
			},
			arch: rockArchAll,
		},
		"wasm as data": {
			files: map[string]string{"lua/app/component.wasm": wasmComponent},
			arch:  rockArchAll,
		},
		"a module of the host": {
			files: map[string]string{
				"lib/app/fast.so":        string(program),
				"lib/app/component.wasm": wasmComponent,
			},
			arch: remote.HostArch(),
		},
		"a module tt cannot read": {
			files: map[string]string{"lib/app/fast.so": "not a binary"},
			arch:  remote.HostArch(),
		},
		"a module of darwin": {
			files: map[string]string{"lib/app/fast.dylib": "not a binary"},
			arch:  remote.HostArch(),
		},
		"a program": {
			files: map[string]string{"bin/tool": string(program)},
			arch:  remote.HostArch(),
		},
		"empty": {files: map[string]string{}, arch: rockArchAll},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stage := t.TempDir()
			writeTree(t, stage, test.files)

			arch, err := rockArch(stage, native.Host())
			require.NoError(t, err)
			assert.Equal(t, test.arch, arch)
		})
	}
}

// TestRockArchOf checks the LuaRocks arch of a platform: that of go-luarocks
// for the machine, and the names LuaRocks gives the others.
func TestRockArchOf(t *testing.T) {
	t.Parallel()

	assert.Equal(t, remote.HostArch(), rockArchOf(native.Host()))

	for platform, arch := range map[native.Platform]string{
		{OS: "linux", Arch: "amd64"}:  "linux-x86_64",
		{OS: "linux", Arch: "arm64"}:  "linux-aarch64",
		{OS: "darwin", Arch: "amd64"}: "macosx-x86_64",
		{OS: "darwin", Arch: "arm64"}: "macosx-aarch64",
	} {
		assert.Equal(t, arch, rockArchOf(platform), platform.String())
	}
}

// TestRockArchOfATarget checks that the rock of a build for another platform
// is the binary rock of that platform, and that a native file of the machine
// in it fails the pack, as one of another platform fails that of the
// machine.
func TestRockArchOfATarget(t *testing.T) {
	t.Parallel()

	other := native.Platform{OS: runtime.GOOS, Arch: "arm64"}
	if runtime.GOARCH == "arm64" {
		other.Arch = "amd64"
	}

	stage := t.TempDir()
	writeTree(t, stage, map[string]string{"lib/app/fast.so": "not a binary"})

	arch, err := rockArch(stage, other)
	require.NoError(t, err)
	assert.Equal(t, rockArchOf(other), arch)

	host, err := os.Executable()
	require.NoError(t, err)

	program, err := os.ReadFile(host)
	require.NoError(t, err)

	writeTree(t, stage, map[string]string{"lib/app/fast.so": string(program)})

	_, err = rockArch(stage, other)
	require.ErrorIs(t, err, errForeignNative)
	assert.Contains(t, err.Error(), "the archive is packed for "+other.String())
}

// wasmLibraryManifest is a library whose build puts a WebAssembly component
// among its modules, into TT_OUTPUT_DIR, beside the Lua of the package.
const wasmLibraryManifest = libraryManifest + `
[components.wasm]
path = 'wasm'

[components.wasm.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'cp component.wasm "$TT_OUTPUT_DIR/component.wasm"']
`

// TestPackRockOfAWasmLibrary checks that a package whose build puts a .wasm
// into lib/ of the rock, and no native file, is an .all rock, as its
// archive packed --without-deps is one of any platform.
func TestPackRockOfAWasmLibrary(t *testing.T) {
	t.Parallel()

	src := writeLibrary(t)
	writeTree(t, src, map[string]string{
		manifestFileName:      wasmLibraryManifest,
		"wasm/component.wasm": wasmComponent,
	})

	out := filepath.Join(src, "_out")

	opts := packOptions(src, out, FormatTt, FormatRock)

	opts.WithoutDeps = true

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(out, "mylib-1.0.0-1.all.rock"), result.Rock)
	assert.Contains(t, zipEntries(t, result.Rock), "lib/mylib/component.wasm")
	assert.True(t, strings.HasSuffix(result.Path, "-any.tt"), result.Path)
}
