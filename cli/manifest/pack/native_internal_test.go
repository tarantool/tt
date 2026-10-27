package pack

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
)

// The detector is fed generated headers rather than binaries built per
// platform: a header is all it reads, generating one takes no toolchain and no
// time, and every generated header is first parsed by the standard library's
// full parser, so the detector is checked against the real layout rather than
// against the generator's idea of it. The test binary itself is the one real
// binary (TestFilePlatformsOfRealBinary).

// elfHeader returns a 64-bit ELF executable header for machine with nothing
// behind it, and checks that debug/elf reads it back as such.
func elfHeader(t *testing.T, machine elf.Machine, abi elf.OSABI, order binary.ByteOrder) []byte {
	t.Helper()

	var hdr elf.Header64

	data := elf.ELFDATA2LSB
	if order == binary.BigEndian {
		data = elf.ELFDATA2MSB
	}

	copy(hdr.Ident[:], elf.ELFMAG)

	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(data)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	hdr.Ident[elf.EI_OSABI] = byte(abi)
	hdr.Type = uint16(elf.ET_EXEC)
	hdr.Machine = uint16(machine)
	hdr.Version = uint32(elf.EV_CURRENT)

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, order, &hdr))

	parsed, err := elf.NewFile(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Equal(t, machine, parsed.Machine)
	require.Equal(t, abi, parsed.OSABI)

	return buf.Bytes()
}

// linuxELF is elfHeader for a little-endian Linux binary.
func linuxELF(t *testing.T, machine elf.Machine) []byte {
	t.Helper()

	return elfHeader(t, machine, elf.ELFOSABI_NONE, binary.LittleEndian)
}

// machoSliceLen is the size of a 64-bit Mach-O header, all machoSlice writes.
const machoSliceLen = 32

// machoSlice returns a thin 64-bit Mach-O executable header for cpu with no
// load commands, and checks that debug/macho reads it back as such.
func machoSlice(t *testing.T, cpu macho.Cpu, order binary.ByteOrder) []byte {
	t.Helper()

	hdr := macho.FileHeader{Magic: macho.Magic64, Cpu: cpu, Type: macho.TypeExec}

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, order, &hdr))
	// The 64-bit header ends with a reserved word FileHeader does not carry.
	require.NoError(t, binary.Write(&buf, order, uint32(0)))
	require.Equal(t, machoSliceLen, buf.Len())

	parsed, err := macho.NewFile(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Equal(t, cpu, parsed.Cpu)

	return buf.Bytes()
}

// fatSliceAlign is where fatMacho places its slices: the n-th one, counting
// from 1, at n*fatSliceAlign, past a fat header of up to a dozen entries.
const fatSliceAlign = 256

// fatMacho returns a universal Mach-O file with one thin slice per cpu, and
// checks that debug/macho reads it back as such.
func fatMacho(t *testing.T, cpus ...macho.Cpu) []byte {
	t.Helper()

	var (
		table, body bytes.Buffer
		narch       uint32
	)

	for _, cpu := range cpus {
		narch++

		require.NoError(t, binary.Write(&table, binary.BigEndian, macho.FatArchHeader{
			Cpu:    cpu,
			Offset: narch * fatSliceAlign,
			Size:   machoSliceLen,
		}))

		body.Write(machoSlice(t, cpu, binary.LittleEndian))
		body.Write(make([]byte, fatSliceAlign-machoSliceLen))
	}

	var file bytes.Buffer

	// The fat header is big-endian whatever the slices are.
	require.NoError(t, binary.Write(&file, binary.BigEndian, macho.MagicFat))
	require.NoError(t, binary.Write(&file, binary.BigEndian, narch))
	file.Write(table.Bytes())
	file.Write(make([]byte, fatSliceAlign-file.Len()))
	file.Write(body.Bytes())

	parsed, err := macho.NewFatFile(bytes.NewReader(file.Bytes()))
	require.NoError(t, err)
	require.Len(t, parsed.Arches, len(cpus))

	for n, cpu := range cpus {
		require.Equal(t, cpu, parsed.Arches[n].Cpu)
	}

	return file.Bytes()
}

// hostNative returns a header native to the host platform, for the tests that
// need a file which passes the check wherever they run.
func hostNative(t *testing.T) []byte {
	t.Helper()

	host := hostPlatform()

	elfMachines := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}
	machoCPUs := map[string]macho.Cpu{"amd64": macho.CpuAmd64, "arm64": macho.CpuArm64}

	switch {
	case host.OS == "linux" && elfMachines[host.Arch] != 0:
		return linuxELF(t, elfMachines[host.Arch])
	case host.OS == darwinOS && machoCPUs[host.Arch] != 0:
		return machoSlice(t, machoCPUs[host.Arch], binary.LittleEndian)
	default:
		t.Skipf("no archive platform for host %s", host)

		return nil
	}
}

// foreignNative returns a header of a platform other than the host's, and
// that platform.
func foreignNative(t *testing.T) ([]byte, platform) {
	t.Helper()

	if hostPlatform() == (platform{OS: "linux", Arch: "arm64"}) {
		return linuxELF(t, elf.EM_X86_64), platform{OS: "linux", Arch: "amd64"}
	}

	return linuxELF(t, elf.EM_AARCH64), platform{OS: "linux", Arch: "arm64"}
}

// platformsOf writes data to a file and runs the detector on it.
func platformsOf(t *testing.T, data []byte) []platform {
	t.Helper()

	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, data, filePerm))

	platforms, err := filePlatforms(path)
	require.NoError(t, err)

	return platforms
}

func TestFilePlatforms(t *testing.T) {
	t.Parallel()

	linux := func(arch string) []platform { return []platform{{OS: "linux", Arch: arch}} }
	darwin := func(arches ...string) []platform {
		platforms := make([]platform, 0, len(arches))
		for _, arch := range arches {
			platforms = append(platforms, platform{OS: darwinOS, Arch: arch})
		}

		return platforms
	}

	little, big := binary.LittleEndian, binary.BigEndian

	tests := []struct {
		name string
		data []byte
		want []platform
	}{
		{"linux amd64", linuxELF(t, elf.EM_X86_64), linux("amd64")},
		{"linux arm64", linuxELF(t, elf.EM_AARCH64), linux("arm64")},
		// GNU is the OS ABI a Linux binary using GNU extensions carries.
		{
			"linux with the gnu os abi",
			elfHeader(t, elf.EM_X86_64, elf.ELFOSABI_LINUX, little),
			linux("amd64"),
		},
		{"linux of another architecture", linuxELF(t, elf.EM_386), linux("386")},
		{"linux arm 32-bit", linuxELF(t, elf.EM_ARM), linux("arm")},
		{
			"big-endian elf",
			elfHeader(t, elf.EM_PPC64, elf.ELFOSABI_NONE, big),
			linux("ppc64"),
		},
		{
			"freebsd does not pass for linux",
			elfHeader(t, elf.EM_X86_64, elf.ELFOSABI_FREEBSD, little),
			[]platform{{OS: "freebsd", Arch: "amd64"}},
		},
		{"darwin amd64", machoSlice(t, macho.CpuAmd64, little), darwin("amd64")},
		{"darwin arm64", machoSlice(t, macho.CpuArm64, little), darwin("arm64")},
		{"big-endian mach-o", machoSlice(t, macho.CpuPpc, big), darwin("ppc")},
		{
			"universal mach-o lists every slice",
			fatMacho(t, macho.CpuAmd64, macho.CpuArm64),
			darwin("amd64", "arm64"),
		},
		{
			"universal mach-o of old architectures",
			fatMacho(t, macho.Cpu386, macho.CpuPpc),
			darwin("386", "ppc"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, platformsOf(t, tt.data))
		})
	}
}

// TestFilePlatformsOfNonNative pins what is not native: anything that is not
// an ELF or Mach-O file, including files that only start like one.
func TestFilePlatformsOfNonNative(t *testing.T) {
	t.Parallel()

	// A Java class file shares the universal Mach-O magic; the word after it
	// is the class format version (52 is Java 8).
	javaClass := []byte{0xca, 0xfe, 0xba, 0xbe, 0x00, 0x00, 0x00, 0x34, 0x00, 0x10}

	// A universal file whose slices are not Mach-O, as in a universal static
	// library, whose slices are ar archives.
	fatOfArchives := fatMacho(t, macho.CpuAmd64, macho.CpuArm64)
	for i := range 2 {
		copy(fatOfArchives[fatSliceAlign*(i+1):], "!<arch>\n")
	}

	// A universal header listing slices the file does not hold.
	truncatedFat := fatMacho(t, macho.CpuAmd64, macho.CpuArm64)[:48]

	tests := map[string][]byte{
		"lua":                      []byte("return {}\n"),
		"shebang script":           []byte("#!/usr/bin/env tarantool\nprint(1)\n"),
		"text":                     []byte("readme\n"),
		"empty":                    {},
		"elf magic only":           []byte(elf.ELFMAG),
		"truncated elf":            []byte("\x7fELF\x02\x01\x01\x00"),
		"ar archive":               []byte("!<arch>\nfoo.o/          0           0     0     644"),
		"java class":               javaClass,
		"universal of ar archives": fatOfArchives,
		"truncated universal":      truncatedFat,
		// A .so is not native for its name: a linker script is one too.
		"linker script": []byte("/* GNU ld script */\nGROUP ( libc.so.6 )\n"),
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Nil(t, platformsOf(t, data))
		})
	}
}

// TestFilePlatformsOfRealBinary runs the detector on a binary a real linker
// produced: the test binary, which is native to the host.
func TestFilePlatformsOfRealBinary(t *testing.T) {
	t.Parallel()

	self, err := os.Executable()
	require.NoError(t, err)

	platforms, err := filePlatforms(self)
	require.NoError(t, err)
	assert.Equal(t, []platform{hostPlatform()}, platforms)
}

func TestNativeFilesScansTheWholeTree(t *testing.T) {
	t.Parallel()

	universal := fatMacho(t, macho.CpuAmd64, macho.CpuArm64)

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app.manifest.toml":                      "manifest_version = '0.1'\n",
		"README.md":                              "readme",
		"bin/helper":                             string(linuxELF(t, elf.EM_X86_64)),
		"bin/run.sh":                             "#!/bin/sh\nexec tarantool\n",
		".rocks/share/tarantool/my-app/init.lua": "return 1",
		".rocks/lib/tarantool/my-app/fast.so":    string(linuxELF(t, elf.EM_AARCH64)),
		".rocks/lib/tarantool/fake/fake.so":      "not a binary",
		"_runtime/tarantool/bin/tarantool":       string(universal),
	})

	// Only regular files reach the archive, so a link to a native file is
	// not one of its native files.
	require.NoError(t, os.Symlink(filepath.Join(root, "bin", "helper"),
		filepath.Join(root, "bin", "helper-link")))

	files, err := nativeFiles(root)
	require.NoError(t, err)

	assert.Equal(t, []nativeFile{
		{Name: ".rocks/lib/tarantool/my-app/fast.so", Platforms: []platform{{"linux", "arm64"}}},
		{
			Name:      "_runtime/tarantool/bin/tarantool",
			Platforms: []platform{{darwinOS, "amd64"}, {darwinOS, "arm64"}},
		},
		{Name: "bin/helper", Platforms: []platform{{"linux", "amd64"}}},
	}, files)
}

func TestNativeFilesOfPureLuaTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".rocks/share/tarantool/my-app/init.lua": "return 1",
		"README.md":                              "readme",
	})

	files, err := nativeFiles(root)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestCheckNativeFiles(t *testing.T) {
	t.Parallel()

	linuxAmd64 := platform{OS: "linux", Arch: "amd64"}
	linuxArm64 := platform{OS: "linux", Arch: "arm64"}
	darwinAmd64 := platform{OS: darwinOS, Arch: "amd64"}
	darwinArm64 := platform{OS: darwinOS, Arch: "arm64"}

	files := []nativeFile{
		{Name: "bin/helper", Platforms: []platform{linuxAmd64}},
		{Name: ".rocks/lib/tarantool/my-app/fast.so", Platforms: []platform{linuxArm64}},
		{Name: "bin/universal", Platforms: []platform{darwinAmd64, darwinArm64}},
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

		target := platform{OS: "linux", Arch: "riscv64"}

		err := checkNativeFiles(files[:1], target)
		require.ErrorIs(t, err, errForeignNative)
		assert.Contains(t, err.Error(), "packed for linux-riscv64")
		assert.NotContains(t, err.Error(), "host")
	})

	t.Run("the host target is called the host's", func(t *testing.T) {
		t.Parallel()

		_, foreign := foreignNative(t)

		err := checkNativeFiles(
			[]nativeFile{{Name: "bin/helper", Platforms: []platform{foreign}}}, hostPlatform())
		require.ErrorIs(t, err, errForeignNative)
		assert.Contains(t, err.Error(),
			"packed for "+hostPlatform().String()+", the host platform")
	})
}
