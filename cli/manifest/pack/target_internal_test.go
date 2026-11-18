package pack

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// This file checks a pack of a build for another platform than the machine
// (build.Options.Target): the rock and the archive are labeled with it.

// otherPlatform is a platform other than the machine of the test, of Linux:
// a module of the build is an ELF file of its machine (elfHead).
func otherPlatform() native.Platform {
	if runtime.GOARCH == "arm64" {
		return native.Platform{OS: "linux", Arch: "amd64"}
	}

	return native.Platform{OS: "linux", Arch: "arm64"}
}

// elfHead is the head of an ELF shared object of Linux of the architecture
// of platform: what tt reads a native file of a platform by.
func elfHead(platform native.Platform) string {
	machine := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}

	// The identification, then e_type and e_machine, two bytes each.
	head := make([]byte, elf.EI_NIDENT+4)

	copy(head, elf.ELFMAG)

	head[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	head[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	head[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(head[elf.EI_NIDENT:], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(head[elf.EI_NIDENT+2:],
		uint16(machine[platform.Arch]))

	return string(head)
}

// targetLibraryManifest is the library with a component whose build copies
// a module of the target into TT_OUTPUT_DIR, as a build for it makes one.
const targetLibraryManifest = libraryManifest + `
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'cp fast.so "$TT_OUTPUT_DIR/fast.so"']
`

// writeTargetLibrary writes the project of targetLibraryManifest with a
// module of platform.
func writeTargetLibrary(t *testing.T, platform native.Platform) string {
	t.Helper()

	src := writeLibrary(t)
	writeTree(t, src, map[string]string{
		manifestFileName: targetLibraryManifest,
		"fast/fast.so":   elfHead(platform),
	})

	return src
}

// TestPackOfATarget checks that a pack of a build for another platform makes
// the binary rock of that platform, named by its LuaRocks arch, and an
// archive --without-deps labeled with it, from the tree of the target, which
// holds the module.
func TestPackOfATarget(t *testing.T) {
	t.Parallel()

	other := otherPlatform()
	src := writeTargetLibrary(t, other)
	out := filepath.Join(src, "_out")

	opts := packOptions(src, out, FormatTt, FormatRock)

	opts.WithoutDeps = true
	opts.Build.Target = other

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(out, "mylib-1.0.0-1."+rockArchOf(other)+".rock"), result.Rock)
	assert.Equal(t, elfHead(other), zipEntries(t, result.Rock)["lib/mylib/fast.so"])

	assert.Equal(t, other.String(), result.Token)
	assert.Equal(t, filepath.Join(out, "mylib-1.0.0-"+other.String()+archiveExt), result.Path)
	assert.Equal(t, elfHead(other),
		readArchive(t, result.Path)[".rocks/lib/tarantool/mylib/fast.so"])

	// The module is of the target: it fails the build of a pack for the
	// machine.
	opts.Build.Target = native.Platform{OS: "", Arch: ""}

	_, err = Run(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a module of the build platform: it runs on "+
		other.String()+", but the build is for "+native.Host().String())
}

// TestPackOfATargetWithDeps checks that the .tt archive with its
// dependencies of a build for another platform is refused before anything
// is built, with exit code 1: it would carry the runtime and the
// dependencies of the machine.
func TestPackOfATargetWithDeps(t *testing.T) {
	t.Parallel()

	other := otherPlatform()
	src := writeTargetLibrary(t, other)

	opts := packOptions(src, filepath.Join(src, "_out"), FormatTt)

	opts.Build.Target = other

	_, err := Run(context.Background(), opts)
	require.ErrorIs(t, err, errCrossDeps)
	require.EqualError(t, err, "a .tt archive with its dependencies is packed for the "+
		"machine tt runs on alone, "+native.Host().String()+": its _runtime/ is the Tarantool "+
		"and the tt of this machine, and its .rocks/ the dependencies built for it; pack the "+
		"archive for "+other.String()+" with --without-deps, whose dependencies are installed "+
		"where it is installed, or pack --format rock")
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	assert.NoDirExists(t, filepath.Join(src, buildDirName, "target"))

	// The machine is no other platform.
	opts.Build.Target = native.Host()

	assert.NoError(t, checkFormats(opts))
}
