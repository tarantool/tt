package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The version of a tarantool is read from its file: pack never runs it. It
// runs as root as the pipe of kernel.core_pattern, where the tarantool is
// the executable of the process that crashed, a file any user may have
// made, and root packs the cores of other users, with the executables
// their cores name, so that tarantool --version ran a program of another
// user as root. The signature of a Tarantool executable, the symbol
// tarantool_version (see hasSignature), is one any ELF file can define.

const (
	// tarantoolPackageSymbol is the function of Tarantool that returns its
	// edition, PACKAGE, which --version prints before the version.
	tarantoolPackageSymbol = "tarantool_package"
	// communityPackage and enterprisePackage are the editions of Tarantool.
	communityPackage  = "Tarantool"
	enterprisePackage = "Tarantool Enterprise"
	// maxReturnedString bounds the string read at the address a function
	// returns, and a version string found in the read-only data.
	maxReturnedString = 256
	// maxFunctionCode bounds the code of a function that is decoded.
	maxFunctionCode = 32
)

var (
	// errNoFunction reports a binary that defines no function of the name
	// in .text.
	errNoFunction = errors.New("no function")
	// errNotDecoded reports a function whose code is none of a function that
	// returns the address of a string, as far as it is decoded.
	errNotDecoded = errors.New("cannot decode the function")
	// errNoString reports an address of no C string of the file.
	errNoString = errors.New("no string")
	// errNoVersionString reports a binary whose version cannot be found.
	errNoVersionString = errors.New("no version string")
)

// returnedVersion matches a version tarantool_version returns, PACKAGE_VERSION:
// a release, x.y.z, and what git describe adds to it, -0-g8b9a0f59a95b, or a
// pre-release suffix before that, -entrypoint-36-gdf2e3013e1.
var returnedVersion = regexp.MustCompile(`^\d+\.\d+\.\d+[0-9A-Za-z.+~_-]*$`)

// describedVersion matches a version string of the read-only data of a
// tarantool that is taken for its version: a release as git describe of a
// build of Tarantool tells it, with the count of commits after the tag and
// the abbreviated commit, and a pre-release suffix of the tag or a suffix of
// the build, such as -dirty, around them.
var describedVersion = regexp.MustCompile(
	`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)*-\d+-g[0-9a-f]{7,40}(?:-[0-9A-Za-z.]+)*$`)

// readBinaryVersion returns the version of the tarantool binary as the first
// line of tarantool --version prints it, "Tarantool 3.9.0-0-g8b9a0f59a95b"
// or "Tarantool Enterprise 3.7.0-0-g1f1ec9fdf", read from its file. The
// version is the string tarantool_version returns, which the code of the
// function, decoded, says the address of (see returnedString); a function
// that cannot be decoded, as one of another machine than x86-64 and AArch64,
// falls back to the version string of the read-only data, the only one there
// of the form of describedVersion. The edition is the string
// tarantool_package returns, which only the symbol table names, else
// Tarantool Enterprise of a binary with that string in its read-only data,
// and Tarantool otherwise. A binary of no version found is an error that
// says why of both ways.
func readBinaryVersion(file *elf.File) (string, error) {
	version, err := returnedString(file, tarantoolSignature)
	if err == nil && !returnedVersion.MatchString(version) {
		err = fmt.Errorf("%w: %s returns %q", errNoVersionString, tarantoolSignature, version)
	}

	if err != nil {
		found, scanErr := scanVersion(file)
		if scanErr != nil {
			return "", fmt.Errorf("%w; %w", err, scanErr)
		}

		version = found
	}

	return binaryPackage(file) + " " + version, nil
}

// binaryPackage returns the edition of Tarantool of file (see
// readBinaryVersion).
func binaryPackage(file *elf.File) string {
	name, err := returnedString(file, tarantoolPackageSymbol)
	if err == nil && (name == communityPackage || strings.HasPrefix(name, communityPackage+" ")) {
		return name
	}

	if hasReadOnlyString(file, enterprisePackage) {
		return enterprisePackage
	}

	return communityPackage
}

// textSymbol returns the symbol name that file defines in .text, of its
// symbol table or its dynamic one.
func textSymbol(file *elf.File, name string) (elf.Symbol, bool) {
	for _, table := range []func() ([]elf.Symbol, error){file.Symbols, file.DynamicSymbols} {
		symbols, err := table()
		if err != nil {
			continue
		}

		for _, symbol := range symbols {
			if symbol.Name != name || symbol.Section == elf.SHN_UNDEF ||
				int(symbol.Section) >= len(file.Sections) {
				continue
			}

			if file.Sections[symbol.Section].Name == ".text" {
				return symbol, true
			}
		}
	}

	return elf.Symbol{
		Name: "", Info: 0, Other: 0, HasVersion: false, VersionIndex: 0, Section: 0, Value: 0,
		Size: 0, Version: "", Library: "",
	}, false
}

// returnedString returns the string the function name of file returns, a
// function that returns the address of a string literal, as
// tarantool_version and tarantool_package do: its code is decoded for the
// address, of the machine of the file (see decodeX86Return and
// decodeARM64Return), and the C string at the address is read from the
// file.
func returnedString(file *elf.File, name string) (string, error) {
	symbol, ok := textSymbol(file, name)
	if !ok {
		return "", fmt.Errorf("%w %s in .text", errNoFunction, name)
	}

	text := file.Sections[symbol.Section]
	if symbol.Value < text.Addr || symbol.Value-text.Addr >= text.Size {
		return "", fmt.Errorf("%w %s: %#x is out of .text", errNotDecoded, name, symbol.Value)
	}

	size := min(maxFunctionCode, text.Size-(symbol.Value-text.Addr))
	if symbol.Size != 0 {
		size = min(size, symbol.Size)
	}

	code := make([]byte, size)

	//nolint:gosec // G115: the offset is within .text, whose size the file tells.
	read, err := text.ReadAt(code, int64(symbol.Value-text.Addr))
	if read < len(code) {
		return "", fmt.Errorf("%w %s: cannot read its code: %w", errNotDecoded, name, err)
	}

	var (
		addr    uint64
		decoded bool
	)

	switch file.Machine {
	case elf.EM_X86_64:
		addr, decoded = decodeX86Return(symbol.Value, code)
	case elf.EM_AARCH64:
		addr, decoded = decodeARM64Return(symbol.Value, code)
	default:
		return "", fmt.Errorf("%w %s: the code of %s is not decoded", errNotDecoded, name,
			file.Machine)
	}

	if !decoded {
		return "", fmt.Errorf("%w %s: its code, % x, returns no address of a string",
			errNotDecoded, name, code)
	}

	return cStringAt(file, addr)
}

// x86 instructions of a function that returns a constant address.
var (
	// x86Endbr64 is endbr64, which a function of -fcf-protection begins with.
	x86Endbr64 = []byte{0xf3, 0x0f, 0x1e, 0xfa}
	// x86Frames are push %rbp and mov %rsp,%rbp of either encoding: the
	// frame of a function of -O0 or -fno-omit-frame-pointer.
	x86Frames = [][]byte{{0x55, 0x48, 0x89, 0xe5}, {0x55, 0x48, 0x8b, 0xec}}
	// x86PopFrame is pop %rbp, which ends the frame before ret.
	x86PopFrame = []byte{0x5d}
	// x86Rets are ret and repz ret.
	x86Rets = [][]byte{{0xc3}, {0xf3, 0xc3}}
)

// decodeX86Return returns the address the x86-64 function at entry, of
// the code given, returns, a constant one: mov $imm32,%eax of a static
// executable, lea disp32(%rip),%rax of a PIE or mov $simm32,%rax, then ret,
// after endbr64 and in a frame of %rbp, as a function of -O0 has, when
// there is one. It reports false of any other code.
func decodeX86Return(entry uint64, code []byte) (uint64, bool) {
	rest, _ := bytes.CutPrefix(code, x86Endbr64)

	frame := false

	for _, push := range x86Frames {
		after, found := bytes.CutPrefix(rest, push)
		if found {
			rest, frame = after, true

			break
		}
	}

	// The sizes of the instructions.
	const (
		movEAX    = 5 // The opcode b8, then imm32.
		movRAX    = 7 // The prefix and the opcode 48 c7 c0, then imm32.
		leaRIPRAX = 7 // The prefix and the opcode 48 8d 05, then disp32.
	)

	var addr uint64

	switch {
	case len(rest) >= movEAX && rest[0] == 0xb8:
		addr = uint64(binary.LittleEndian.Uint32(rest[1:movEAX]))
		rest = rest[movEAX:]
	case len(rest) >= movRAX && bytes.HasPrefix(rest, []byte{0x48, 0xc7, 0xc0}):
		//nolint:gosec // G115: the immediate is sign-extended, as the CPU does.
		addr = uint64(int64(int32(binary.LittleEndian.Uint32(rest[3:movRAX]))))
		rest = rest[movRAX:]
	case len(rest) >= leaRIPRAX && bytes.HasPrefix(rest, []byte{0x48, 0x8d, 0x05}):
		// The displacement is from the end of the instruction, which follows
		// the prefixes decoded, fewer bytes than the code.
		//nolint:gosec // G115: the displacement is sign-extended, as the CPU does.
		addr = entry + uint64(len(code)-len(rest)) + leaRIPRAX +
			uint64(int64(int32(binary.LittleEndian.Uint32(rest[3:leaRIPRAX]))))
		rest = rest[leaRIPRAX:]
	default:
		return 0, false
	}

	if frame {
		var popped bool

		rest, popped = bytes.CutPrefix(rest, x86PopFrame)
		if !popped {
			return 0, false
		}
	}

	if !slices.ContainsFunc(x86Rets, func(ret []byte) bool { return bytes.HasPrefix(rest, ret) }) {
		return 0, false
	}

	return addr, true
}

// AArch64 instructions of a function that returns a constant address.
const (
	// arm64BTIC is bti c, which a function of -mbranch-protection begins
	// with.
	arm64BTIC = 0xd503245f
	// arm64PACIASP and arm64AUTIASP sign and check the return address, of a
	// function of -mbranch-protection=pac-ret.
	arm64PACIASP = 0xd503233f
	arm64AUTIASP = 0xd50323bf
	// arm64Ret is ret.
	arm64Ret = 0xd65f03c0
	// arm64ADRMask and its values tell adrp x0 and adr x0: op, the fixed
	// bits and Rd, x0.
	arm64ADRMask = 0x9f00001f
	arm64ADRP    = 0x90000000
	arm64ADR     = 0x10000000
	// arm64AddMask and arm64AddX0 tell add x0,x0,#imm of 64 bits, of any
	// shift of the immediate.
	arm64AddMask = 0xff8003ff
	arm64AddX0   = 0x91000000
	// arm64AddShift is the bit of add that shifts its immediate by 12.
	arm64AddShift = 1 << 22
	// arm64Page is the page adrp addresses by.
	arm64Page = 12
)

// decodeARM64Return returns the address the AArch64 function at entry, of
// the code given, returns, a constant one: adrp x0 then add x0,x0,#imm, or
// adr x0 alone, then ret, after bti c and paciasp, with autiasp before ret,
// when there are. It reports false of any other code.
func decodeARM64Return(entry uint64, code []byte) (uint64, bool) {
	const word = 4

	var words []uint32

	for start := 0; start+word <= len(code); start += word {
		words = append(words, binary.LittleEndian.Uint32(code[start:start+word]))
	}

	index := 0

	for index < len(words) && (words[index] == arm64BTIC || words[index] == arm64PACIASP) {
		index++
	}

	if index >= len(words) {
		return 0, false
	}

	// The address of the instruction, which adr and adrp are relative to.
	address := entry + uint64(index)*word

	var addr uint64

	switch words[index] & arm64ADRMask {
	case arm64ADRP:
		if index+1 >= len(words) || words[index+1]&arm64AddMask != arm64AddX0 {
			return 0, false
		}

		add := words[index+1]
		imm := uint64((add >> 10) & 0xfff) //nolint:mnd // imm12 of add.

		if add&arm64AddShift != 0 {
			imm <<= arm64Page
		}

		//nolint:gosec // G115: the offset of pages is signed.
		addr = address&^(1<<arm64Page-1) + uint64(adrOffset(words[index])<<arm64Page) + imm

		index += 2
	case arm64ADR:
		//nolint:gosec // G115: the offset is signed.
		addr = address + uint64(adrOffset(words[index]))

		index++
	default:
		return 0, false
	}

	if index < len(words) && words[index] == arm64AUTIASP {
		index++
	}

	if index >= len(words) || words[index] != arm64Ret {
		return 0, false
	}

	return addr, true
}

// adrOffset returns the signed immediate of adr or adrp, immhi:immlo of 21
// bits: of bytes for adr, of pages for adrp.
func adrOffset(instruction uint32) int64 {
	immlo := (instruction >> 29) & 0x3    //nolint:mnd // Bits 30:29.
	immhi := (instruction >> 5) & 0x7ffff //nolint:mnd // Bits 23:5.

	return int64(immhi<<2|immlo) << 43 >> 43 //nolint:mnd // Sign-extends the 21 bits.
}

// cStringAt returns the C string at the address addr of file, read from the
// file image of the PT_LOAD segment that has it, of maxReturnedString bytes
// at most, of printable ASCII.
func cStringAt(file *elf.File, addr uint64) (string, error) {
	for _, prog := range file.Progs {
		if prog.Type != elf.PT_LOAD || addr < prog.Vaddr || addr-prog.Vaddr >= prog.Filesz {
			continue
		}

		data := make([]byte, min(maxReturnedString, prog.Filesz-(addr-prog.Vaddr)))

		//nolint:gosec // G115: the offset is within the segment, whose size the file tells.
		read, err := prog.ReadAt(data, int64(addr-prog.Vaddr))
		if read < len(data) {
			return "", fmt.Errorf("%w at %#x: %w", errNoString, addr, err)
		}

		text, _, found := bytes.Cut(data, []byte{0})
		if !found || !printable(text) {
			return "", fmt.Errorf("%w at %#x: no C string of printable characters of %d bytes "+
				"at most", errNoString, addr, maxReturnedString)
		}

		return string(text), nil
	}

	return "", fmt.Errorf("%w at %#x: no segment of the file has it", errNoString, addr)
}

// printable reports whether text is printable ASCII.
func printable(text []byte) bool {
	return !slices.ContainsFunc(text, func(char byte) bool { return char < ' ' || char > '~' })
}

// readOnlyData returns the data of the sections of file that a process of
// it maps read-only, and that are no code: .rodata and the like.
func readOnlyData(file *elf.File) [][]byte {
	var data [][]byte

	for _, section := range file.Sections {
		if section.Type != elf.SHT_PROGBITS || section.Flags&elf.SHF_ALLOC == 0 ||
			section.Flags&(elf.SHF_WRITE|elf.SHF_EXECINSTR) != 0 {
			continue
		}

		content, err := section.Data()
		if err == nil {
			data = append(data, content)
		}
	}

	return data
}

// scanVersion returns the version string of the read-only data of file, the
// only C string there of the form of describedVersion, which
// PACKAGE_VERSION of a build of Tarantool from git is. None, or more than
// one, is an error.
func scanVersion(file *elf.File) (string, error) {
	var found []string

	for _, data := range readOnlyData(file) {
		for len(data) > 0 {
			text, rest, terminated := bytes.Cut(data, []byte{0})
			if !terminated {
				break
			}

			// A version begins with a digit: the rest are not matched.
			if len(text) > 0 && len(text) <= maxReturnedString && text[0] >= '0' &&
				text[0] <= '9' && describedVersion.Match(text) &&
				!slices.Contains(found, string(text)) {
				found = append(found, string(text))
			}

			data = rest
		}
	}

	switch len(found) {
	case 0:
		return "", fmt.Errorf("%w of the form x.y.z-N-gCOMMIT in the read-only data",
			errNoVersionString)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%w alone in the read-only data: %d of the form x.y.z-N-gCOMMIT, %s",
			errNoVersionString, len(found), strings.Join(found, ", "))
	}
}

// hasReadOnlyString reports whether the read-only data of file has the C
// string text, whole.
func hasReadOnlyString(file *elf.File, text string) bool {
	whole := []byte(text + "\x00")

	for _, data := range readOnlyData(file) {
		if bytes.HasPrefix(data, whole) || bytes.Contains(data, append([]byte{0}, whole...)) {
			return true
		}
	}

	return false
}
