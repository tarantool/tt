package pack

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// The detector itself is tested in cli/manifest/native; pack only needs a
// file the detector takes for one of a platform, and a header is all it
// reads.

// linuxELF returns a little-endian 64-bit Linux ELF executable header for
// machine with nothing behind it.
func linuxELF(t *testing.T, machine elf.Machine) []byte {
	t.Helper()

	var hdr elf.Header64

	copy(hdr.Ident[:], elf.ELFMAG)

	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	hdr.Type = uint16(elf.ET_EXEC)
	hdr.Machine = uint16(machine)
	hdr.Version = uint32(elf.EV_CURRENT)

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, binary.LittleEndian, &hdr))

	return buf.Bytes()
}

// machoSlice returns a thin little-endian 64-bit Mach-O executable header for
// cpu with no load commands.
func machoSlice(t *testing.T, cpu macho.Cpu) []byte {
	t.Helper()

	hdr := macho.FileHeader{Magic: macho.Magic64, Cpu: cpu, Type: macho.TypeExec}

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, binary.LittleEndian, &hdr))
	// The 64-bit header ends with a reserved word FileHeader does not carry.
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint32(0)))

	return buf.Bytes()
}

// hostNative returns a header native to the host platform, for the tests that
// need a file which passes the check wherever they run.
func hostNative(t *testing.T) []byte {
	t.Helper()

	host := native.Host()

	elfMachines := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}
	machoCPUs := map[string]macho.Cpu{"amd64": macho.CpuAmd64, "arm64": macho.CpuArm64}

	switch {
	case host.OS == "linux" && elfMachines[host.Arch] != 0:
		return linuxELF(t, elfMachines[host.Arch])
	case host.OS == "darwin" && machoCPUs[host.Arch] != 0:
		return machoSlice(t, machoCPUs[host.Arch])
	default:
		t.Skipf("no archive platform for host %s", host)

		return nil
	}
}

// foreignNative returns a header of a platform other than the host's, and
// that platform.
func foreignNative(t *testing.T) ([]byte, native.Platform) {
	t.Helper()

	if native.Host() == (native.Platform{OS: "linux", Arch: "arm64"}) {
		return linuxELF(t, elf.EM_X86_64), native.Platform{OS: "linux", Arch: "amd64"}
	}

	return linuxELF(t, elf.EM_AARCH64), native.Platform{OS: "linux", Arch: "arm64"}
}

func TestCheckNativeFiles(t *testing.T) {
	t.Parallel()

	linuxAmd64 := native.Platform{OS: "linux", Arch: "amd64"}
	linuxArm64 := native.Platform{OS: "linux", Arch: "arm64"}
	darwinAmd64 := native.Platform{OS: "darwin", Arch: "amd64"}
	darwinArm64 := native.Platform{OS: "darwin", Arch: "arm64"}

	files := []native.File{
		{Name: "bin/helper", Platforms: []native.Platform{linuxAmd64}},
		{Name: ".rocks/lib/tarantool/my-app/fast.so", Platforms: []native.Platform{linuxArm64}},
		{Name: "bin/universal", Platforms: []native.Platform{darwinAmd64, darwinArm64}},
	}

	t.Run("no native file passes any target", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, checkNativeFiles(nil, linuxAmd64))
	})

	t.Run("files of the target pass", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, checkNativeFiles(files[:1], linuxAmd64))
	})

	t.Run("a universal file passes on any of its slices", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, checkNativeFiles(files[2:], darwinAmd64))
		require.NoError(t, checkNativeFiles(files[2:], darwinArm64))
	})

	t.Run("every foreign file is named at once", func(t *testing.T) {
		t.Parallel()

		err := checkNativeFiles(files, linuxAmd64)
		require.Error(t, err)
		require.ErrorIs(t, err, errForeignNative)
		assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))

		lines := strings.Split(err.Error(), "\n")
		require.Len(t, lines, 2, err.Error())

		assert.Contains(t, lines[0], ".rocks/lib/tarantool/my-app/fast.so")
		assert.Contains(t, lines[0], "runs on linux-arm64")
		assert.Contains(t, lines[0], "packed for linux-amd64")
		assert.Contains(t, lines[1], "bin/universal")
		assert.Contains(t, lines[1], "runs on darwin-amd64 and darwin-arm64")
		assert.NotContains(t, err.Error(), "bin/helper")
	})

	// The check takes the platform the archive is packed for, not the host's,
	// so the day pack builds for another platform it checks against that one.
	t.Run("the target need not be the host", func(t *testing.T) {
		t.Parallel()

		target := native.Platform{OS: "linux", Arch: "riscv64"}

		err := checkNativeFiles(files[:1], target)
		require.ErrorIs(t, err, errForeignNative)
		assert.Contains(t, err.Error(), "packed for linux-riscv64")
		assert.NotContains(t, err.Error(), "host")
	})

	t.Run("the host target is called the host's", func(t *testing.T) {
		t.Parallel()

		_, foreign := foreignNative(t)

		err := checkNativeFiles(
			[]native.File{{Name: "bin/helper", Platforms: []native.Platform{foreign}}},
			native.Host())
		require.ErrorIs(t, err, errForeignNative)
		assert.Contains(t, err.Error(),
			"packed for "+native.Host().String()+", the host platform")
	})
}
