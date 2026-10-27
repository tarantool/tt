package pack

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Native files are told by their content, not by their name. A compiled Lua
// module is a .so, but it is not the only file that pins a package to a
// platform: an executable from [package].include or a binary a hook built
// usually has no extension at all, and an archive carrying a linux-amd64 one
// labeled "any" installs on darwin or arm64 and fails only when the instance
// starts.
//
// Only the header is read. The bundled runtime's binaries are tens of
// megabytes, and debug/elf and debug/macho parse far more than the platform:
// macho.NewFatFile in particular loads every slice's load commands and symbol
// table. The fixed-size header fields are decoded here instead, with the
// standard library's constants and types.
const (
	// elfIdentLen is how much of an ELF file names its platform: the 16-byte
	// e_ident, then e_type and e_machine. The layout up to e_machine is the
	// same for 32- and 64-bit files.
	elfIdentLen = 20
	// elfMachineOffset is where e_machine starts.
	elfMachineOffset = 18
	// machoIdentLen is the magic and cputype of a thin Mach-O header, and the
	// magic and nfat_arch of a universal one.
	machoIdentLen = 8
	// fatArchLen is the size of one fat_arch entry (macho.FatArchHeader).
	fatArchLen = 20
	// maxFatArches bounds the slices a universal Mach-O may list. Real ones
	// carry two or three. The bound also tells a Java class file, which shares
	// the 0xcafebabe magic, from a universal binary: its next word holds the
	// class format version, 45 and up.
	maxFatArches = 32
)

// darwinOS is the OS of every Mach-O file.
const darwinOS = "darwin"

// nativeFile is a native file of the staged tree: an ELF or Mach-O file,
// whatever its name.
type nativeFile struct {
	// Name is the file's path inside the archive, slash-separated.
	Name string
	// Platforms lists the platforms the file runs on: one for a thin binary,
	// one per slice for a universal Mach-O.
	Platforms []platform
}

// runsOn reports whether the file runs on target. A universal Mach-O runs
// wherever one of its slices does.
func (f nativeFile) runsOn(target platform) bool {
	return slices.Contains(f.Platforms, target)
}

// nativeFiles lists every native file under root, in lexical order, with the
// platforms each runs on. It does not judge them: checkNativeFiles compares
// them with the platform the archive is packed for.
//
// The whole tree is looked at - the [package].include payload, .rocks/ and
// _runtime/ alike. The bundled runtime is no exception: the runtime cache has
// no platform in its layout, so a build of another platform placed there would
// otherwise travel under the host's label.
func nativeFiles(root string) ([]nativeFile, error) {
	var found []nativeFile

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Only regular files reach the archive (see walkIntoTar), so a symlink
		// is not followed here either.
		if !entry.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		name := filepath.ToSlash(rel)

		platforms, err := filePlatforms(path)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		if platforms != nil {
			found = append(found, nativeFile{Name: name, Platforms: platforms})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning for native files: %w", err)
	}

	return found, nil
}

// checkNativeFiles fails on every native file that does not run on target,
// naming each with the platforms it does run on, so a tree with several is
// fixed in one pass. An archive is labeled with one platform: a native file of
// another one would fail only once the archive is installed and started, and
// pack cannot rebuild it for the archive's platform.
func checkNativeFiles(files []nativeFile, target platform) error {
	var problems []error

	for _, file := range files {
		if file.runsOn(target) {
			continue
		}

		problems = append(problems, fmt.Errorf(
			"%s: %w: it runs on %s, but the archive is packed for %s",
			file.Name, errForeignNative, describePlatforms(file.Platforms),
			describeTarget(target)))
	}

	joined := errors.Join(problems...)
	if joined == nil {
		return nil
	}

	return stateErrorf("%w", joined)
}

// describePlatforms renders the platforms of a native file for a message:
// "linux-arm64", or "darwin-amd64 and darwin-arm64" for a universal one.
func describePlatforms(platforms []platform) string {
	names := make([]string, 0, len(platforms))
	for _, p := range platforms {
		names = append(names, p.String())
	}

	last := len(names) - 1
	if last < 1 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:last], ", ") + " and " + names[last]
}

// describeTarget renders the platform an archive is packed for, saying when
// it is the host's: the archive is never labeled anything else today, and a
// user reading the error would otherwise wonder where the platform came from.
func describeTarget(target platform) string {
	if target == hostPlatform() {
		return target.String() + ", the host platform"
	}

	return target.String()
}

// filePlatforms reports the platforms the file at path runs on, reading only
// its header. A file that is neither ELF nor Mach-O - Lua, a shebang script,
// text - is not native and yields nil, and so does one that only starts like
// a binary and is too short or malformed to be one.
func filePlatforms(path string) ([]platform, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller names the file.
	}

	defer func() { _ = file.Close() }()

	return readPlatforms(file)
}

// readPlatforms is filePlatforms over an open file.
func readPlatforms(src io.ReaderAt) ([]platform, error) {
	head := make([]byte, elfIdentLen)

	n, err := src.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err //nolint:wrapcheck // The caller names the file.
	}

	head = head[:n]

	if bytes.HasPrefix(head, []byte(elf.ELFMAG)) {
		return elfPlatforms(head), nil
	}

	if len(head) < machoIdentLen {
		return nil, nil
	}

	// The fat header is big-endian whatever the slices are.
	if binary.BigEndian.Uint32(head) == macho.MagicFat {
		return fatPlatforms(src, binary.BigEndian.Uint32(head[4:]))
	}

	cpu, ok := machoCPU(head)
	if !ok {
		return nil, nil
	}

	return []platform{{OS: darwinOS, Arch: machoArch(cpu)}}, nil
}

// elfPlatforms decodes the platform of an ELF file from its first
// elfIdentLen bytes.
func elfPlatforms(head []byte) []platform {
	if len(head) < elfIdentLen {
		return nil
	}

	var order binary.ByteOrder

	switch elf.Data(head[elf.EI_DATA]) {
	case elf.ELFDATA2LSB:
		order = binary.LittleEndian
	case elf.ELFDATA2MSB:
		order = binary.BigEndian
	default:
		return nil
	}

	machine := elf.Machine(order.Uint16(head[elfMachineOffset:]))

	return []platform{{
		OS:   elfOS(elf.OSABI(head[elf.EI_OSABI])),
		Arch: elfArch(machine),
	}}
}

// elfOS names the OS an ELF file is for. A Linux binary carries no OS ABI of
// its own (ELFOSABI_NONE) or the GNU one. Another OS that marks its binaries
// here - FreeBSD does - is named, so that its binary does not pass for a Linux
// one; the BSDs that mark theirs with a note only are taken for Linux, since
// the note lies past the header.
func elfOS(abi elf.OSABI) string {
	switch abi {
	case elf.ELFOSABI_NONE, elf.ELFOSABI_LINUX:
		return "linux"
	default:
		return shortName(abi.String(), "ELFOSABI_")
	}
}

// elfArch names the architecture of an ELF file in GOARCH spelling. Only the
// two the archive platforms use are mapped; any other can only mismatch, and
// its name is the standard library's, for the message.
func elfArch(machine elf.Machine) string {
	switch machine {
	case elf.EM_X86_64:
		return "amd64"
	case elf.EM_AARCH64:
		return "arm64"
	default:
		return shortName(machine.String(), "EM_")
	}
}

// machoCPU reads the CPU type of a thin Mach-O header, in whichever byte order
// its magic says the file is in.
func machoCPU(head []byte) (macho.Cpu, bool) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		switch order.Uint32(head) {
		case macho.Magic32, macho.Magic64:
			return macho.Cpu(order.Uint32(head[4:])), true
		}
	}

	return 0, false
}

// machoArch names the architecture of a Mach-O CPU type in GOARCH spelling,
// the same way elfArch does.
func machoArch(cpu macho.Cpu) string {
	switch cpu {
	case macho.CpuAmd64:
		return "amd64"
	case macho.CpuArm64:
		return "arm64"
	default:
		return shortName(cpu.String(), "Cpu")
	}
}

// fatPlatforms reads the slices of a universal Mach-O file whose header lists
// narch of them. Each slice's own header is checked to be a Mach-O one: a
// file that merely starts with the fat magic is not taken for a binary, and a
// universal static library, whose slices are ar archives, is no more native
// than a thin one.
func fatPlatforms(src io.ReaderAt, narch uint32) ([]platform, error) {
	if narch == 0 || narch > maxFatArches {
		return nil, nil
	}

	arches := make([]macho.FatArchHeader, narch)
	table := io.NewSectionReader(src, machoIdentLen, int64(narch)*fatArchLen)

	err := binary.Read(table, binary.BigEndian, arches)
	if err != nil {
		return nil, shortRead(err)
	}

	var platforms []platform

	for _, arch := range arches {
		slice := make([]byte, machoIdentLen)

		_, err = src.ReadAt(slice, int64(arch.Offset))
		if err != nil {
			return nil, shortRead(err)
		}

		cpu, ok := machoCPU(slice)
		if !ok {
			return nil, nil
		}

		p := platform{OS: darwinOS, Arch: machoArch(cpu)}
		if !slices.Contains(platforms, p) {
			platforms = append(platforms, p)
		}
	}

	return platforms, nil
}

// shortRead turns a read that ran past the end of the file into "not
// native": a file too short for the header it claims is no binary.
func shortRead(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}

	return err
}

// shortName turns a debug/elf or debug/macho constant name into the short
// lowercase one a message shows: EM_386 to 386, ELFOSABI_FREEBSD to freebsd,
// CpuPpc to ppc. A value the standard library has no name for stays a
// number.
func shortName(name, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(name, prefix))
}
