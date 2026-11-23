package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
)

// The types of the notes Linux writes into a core dump that pack reads, of
// the owner CORE (see include/uapi/linux/elf.h of Linux). gcore of gdb
// writes them too.
const (
	// ntPrstatus is the status of a thread, struct elf_prstatus: one note
	// a thread, with its pid and its registers.
	ntPrstatus = 1
	// ntPrpsinfo is the information of the process, struct elf_prpsinfo,
	// with its pid.
	ntPrpsinfo = 3
	// ntAuxv is the auxiliary vector of the process.
	ntAuxv = 6
	// ntFile lists the files mapped into the memory of the process.
	ntFile = 0x46494c45
)

// coreOwner is the owner of the notes of a core dump as a note names it,
// with the terminating NUL.
const coreOwner = "CORE\x00"

// The types of the entries of the auxiliary vector pack reads (see
// getauxval(3)).
const (
	// atNull ends the vector.
	atNull = 0
	// atPhdr is the address of the program headers of the executable.
	atPhdr = 3
	// atBase is the address the kernel loaded the interpreter of the
	// executable at, the dynamic loader, or 0 without one.
	atBase = 7
)

// maxNoteSize bounds what is read of a note segment of an ELF image in the
// memory of a core, against a corrupt header: a segment of the build ID
// takes a few dozen bytes.
const maxNoteSize = 1 << 16

// maxDynamicSize bounds what is read of the dynamic section of an ELF image
// in the memory of a core: that of a tarantool takes 560 bytes.
const maxDynamicSize = 1 << 20

// maxPathSize bounds a path read from the memory of a core: PATH_MAX of
// Linux, with the NUL.
const maxPathSize = 4096

// maxLinkMap bounds the walk of the link map of a core, against a corrupt
// one: a tarantool loads a few dozen objects.
const maxLinkMap = 1 << 16

var (
	// errNotInCore reports memory of the process the core dump holds no
	// content of: Linux leaves out the pages of a file mapping the process
	// did not write to, but for the first one of an ELF file.
	errNotInCore = errors.New("the core dump holds no content at the address")
	// errMalformedNote reports a note of a core dump cut short.
	errMalformedNote = errors.New("malformed note")
	// errNotCoreFile reports an ELF file of another type than a core dump.
	errNotCoreFile = errors.New("not an ELF core file")
	// errNoImage reports an address of the memory of a core dump that holds
	// no ELF image.
	errNoImage = errors.New("no ELF image")
	// errLinkMapLoop reports a link map of a core dump that links back to
	// an entry of it.
	errLinkMapLoop = errors.New("the link map loops")
	// errLinkMapLong reports a link map of a core dump of more entries than
	// maxLinkMap, as one corrupt enough not to end is.
	errLinkMapLong = errors.New("the link map is too long")
	// errLongString reports a string of the memory of a core dump longer
	// than a path.
	errLongString = errors.New("string too long")
)

// elfNote is a note of an ELF file.
type elfNote struct {
	// name is the owner of the note, with its terminating NUL.
	name string
	// typ is the type of the note, which the owner defines.
	typ uint32
	// desc is the descriptor of the note, its content.
	desc []byte
}

// parseNotes returns the notes of data, the content of a note section or
// segment aligned to align: 4 bytes unless it says 8. The descriptor of a
// note starts at its header and its name padded together to the alignment,
// and the next note after the descriptor padded, as readelf reads them: for
// 4 bytes it is the name that is padded, the header taking 12. A note cut
// short ends the list.
func parseNotes(data []byte, order binary.ByteOrder, align uint64) []elfNote {
	pad := uint64(4) //nolint:mnd // The alignment of the notes of ELF.
	if align == 8 {  //nolint:mnd // A segment of 8-byte aligned notes.
		pad = align
	}

	padded := func(size uint64) uint64 {
		return (size + pad - 1) / pad * pad
	}

	var notes []elfNote

	for uint64(len(data)) >= noteHeaderSize {
		nameSize := uint64(order.Uint32(data[0:4]))
		descSize := uint64(order.Uint32(data[4:8]))

		nameEnd := padded(noteHeaderSize + nameSize)
		if nameEnd+descSize > uint64(len(data)) {
			break
		}

		notes = append(notes, elfNote{
			name: string(data[noteHeaderSize : noteHeaderSize+nameSize]),
			typ:  order.Uint32(data[8:12]),
			desc: data[nameEnd : nameEnd+descSize],
		})

		descEnd := padded(nameEnd + descSize)
		if descEnd > uint64(len(data)) {
			break
		}

		data = data[descEnd:]
	}

	return notes
}

// fileMapping is a file mapped into the memory of a process, an entry of
// the NT_FILE note of its core dump.
type fileMapping struct {
	// start and end are the addresses of the mapping, end excluded.
	start, end uint64
	// offset is the offset in the file of the byte mapped at start.
	offset uint64
	// path is the path of the file as Linux records it, with " (deleted)"
	// appended to the path of a file removed since it was mapped.
	path string
}

// elfImage is an ELF file the memory of a process holds: its entry point
// and its program headers, and the difference of the addresses it is
// mapped at to those the headers give.
type elfImage struct {
	bias  uint64
	entry uint64
	progs []elf.ProgHeader
}

// linkMapEntry is an object the dynamic loader of a process loaded, an
// entry of its link map.
type linkMapEntry struct {
	// name is the path the loader opened the object by, and the name of
	// the vDSO for it: empty when it cannot be read.
	name string
	// nameErr is why the name cannot be read, or nil.
	nameErr error
	// addr is the difference of the addresses the object is loaded at to
	// those of its file, l_addr: where a shared library is loaded.
	addr uint64
	// dynamic is the address of the dynamic section of the object.
	dynamic uint64
}

// fileSegment is memory of a process whose content is that of a file: a
// segment of an ELF file mapped at vaddr that the process cannot write to.
type fileSegment struct {
	vaddr, size uint64
	data        io.ReaderAt
}

// coreDump is a core dump of a process of Linux, open for reading: the
// memory of the process the dump holds, the files the process had mapped
// and its auxiliary vector.
type coreDump struct {
	file *elf.File
	// loads are the segments of memory that have content, by address.
	loads []*elf.Prog
	// files are the segments of memory read from a file where the core
	// holds no content, those of the binary of withBinary.
	files []fileSegment
	// mappings are the files mapped, by address.
	mappings []fileMapping
	// fileNoteErr is why the NT_FILE note, which the mappings come from, is
	// unread, or nil.
	fileNoteErr error
	// auxv is the auxiliary vector, by the type of an entry.
	auxv map[uint64]uint64
	// threads are the descriptors of the NT_PRSTATUS notes, one a thread,
	// in their order.
	threads [][]byte
	// process is the descriptor of the NT_PRPSINFO note, nil without one.
	process []byte
}

// openCore opens the core dump at path. A file that is no ELF core dump is
// an error. A core without the notes of the files mapped or of the
// auxiliary vector, as one of a Linux older than 3.7 is, has no mappings
// or no auxiliary vector, and so does one whose NT_FILE note is malformed,
// as gdb and file(1) take it, with fileNoteErr set.
func openCore(path string) (*coreDump, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the ELF file: %w", err)
	}

	core := &coreDump{
		file: file, loads: nil, files: nil, mappings: nil, fileNoteErr: nil,
		auxv: map[uint64]uint64{}, threads: nil, process: nil,
	}

	err = core.load()
	if err != nil {
		_ = file.Close()

		return nil, err
	}

	return core, nil
}

// Close closes the file of the core.
func (c *coreDump) Close() error {
	err := c.file.Close()
	if err != nil {
		return fmt.Errorf("cannot close the core dump: %w", err)
	}

	return nil
}

// load reads the segments of memory and the notes of the core.
func (c *coreDump) load() error {
	if c.file.Type != elf.ET_CORE {
		return fmt.Errorf("%w: an ELF file of type %s", errNotCoreFile, c.file.Type)
	}

	for _, prog := range c.file.Progs {
		switch prog.Type {
		case elf.PT_LOAD:
			if prog.Filesz > 0 {
				c.loads = append(c.loads, prog)
			}
		case elf.PT_NOTE:
			data, err := io.ReadAll(prog.Open())
			if err != nil {
				return fmt.Errorf("cannot read the notes: %w", err)
			}

			c.loadNotes(parseNotes(data, c.file.ByteOrder, prog.Align))
		default:
		}
	}

	slices.SortFunc(c.loads, func(a, b *elf.Prog) int {
		return compareAddresses(a.Vaddr, b.Vaddr)
	})
	slices.SortFunc(c.mappings, func(a, b fileMapping) int {
		return compareAddresses(a.start, b.start)
	})

	return nil
}

// compareAddresses orders the addresses a and b.
func compareAddresses(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// loadNotes reads the notes of the files mapped and of the auxiliary vector
// among notes, and keeps those of the threads and of the process. A
// malformed NT_FILE note is left out, and fileNoteErr says why.
func (c *coreDump) loadNotes(notes []elfNote) {
	for _, note := range notes {
		if note.name != coreOwner {
			continue
		}

		switch note.typ {
		case ntPrstatus:
			c.threads = append(c.threads, note.desc)
		case ntPrpsinfo:
			c.process = note.desc
		case ntFile:
			mappings, err := c.parseFileNote(note.desc)
			if err != nil {
				c.fileNoteErr = err

				continue
			}

			c.mappings = append(c.mappings, mappings...)
		case ntAuxv:
			c.parseAuxv(note.desc)
		default:
		}
	}
}

// wordSize is the size of a word of the process, of a long of C.
func (c *coreDump) wordSize() uint64 {
	if c.file.Class == elf.ELFCLASS32 {
		return 4 //nolint:mnd // The size of a word of a 32-bit process.
	}

	return 8 //nolint:mnd // The size of a word of a 64-bit process.
}

// word decodes the word data begins with.
func (c *coreDump) word(data []byte) uint64 {
	if c.file.Class == elf.ELFCLASS32 {
		return uint64(c.file.ByteOrder.Uint32(data))
	}

	return c.file.ByteOrder.Uint64(data)
}

// parseFileNote decodes the NT_FILE note desc: the number of the mappings
// and the size of a page, the start, the end and the offset in pages of
// each mapping, then the paths of their files, each ended by a NUL.
func (c *coreDump) parseFileNote(desc []byte) ([]fileMapping, error) {
	word := c.wordSize()
	size := uint64(len(desc))

	if size < 2*word {
		return nil, fmt.Errorf("%w: NT_FILE of %d bytes", errMalformedNote, size)
	}

	count, pageSize := c.word(desc), c.word(desc[word:])

	const fields = 3 // The start, the end and the offset.

	if count > (size-2*word)/(fields*word) {
		return nil, fmt.Errorf("%w: NT_FILE of %d bytes for %d files", errMalformedNote, size,
			count)
	}

	// Each path ends with a NUL: count of them split the names into one
	// part more.
	names := desc[2*word+count*fields*word:]
	paths := strings.SplitN(string(names), "\x00", int(count)+1) //nolint:gosec // Bounded above.

	if uint64(len(paths)) <= count {
		return nil, fmt.Errorf("%w: NT_FILE names %d of %d files", errMalformedNote, len(paths),
			count)
	}

	mappings := make([]fileMapping, 0, count)

	for index := range count {
		entry := desc[2*word+index*fields*word:]

		mappings = append(mappings, fileMapping{
			start:  c.word(entry),
			end:    c.word(entry[word:]),
			offset: c.word(entry[2*word:]) * pageSize,
			path:   paths[index],
		})
	}

	return mappings, nil
}

// parseAuxv decodes the NT_AUXV note desc: pairs of words, the type and
// the value of an entry, up to the entry AT_NULL.
func (c *coreDump) parseAuxv(desc []byte) {
	word := c.wordSize()

	for uint64(len(desc)) >= 2*word {
		typ := c.word(desc)
		if typ == atNull {
			return
		}

		c.auxv[typ] = c.word(desc[word:])
		desc = desc[2*word:]
	}
}

// loadAt returns the segment of memory with content at addr, or nil.
func (c *coreDump) loadAt(addr uint64) *elf.Prog {
	index, found := slices.BinarySearchFunc(c.loads, addr, func(prog *elf.Prog, addr uint64) int {
		return compareAddresses(prog.Vaddr, addr)
	})
	if !found {
		// The segment before the first one that starts above addr.
		if index == 0 {
			return nil
		}

		index--
	}

	prog := c.loads[index]
	if addr-prog.Vaddr >= prog.Filesz {
		return nil
	}

	return prog
}

// read returns size bytes of the memory of the process at addr, which may
// span segments. Memory the core holds no content of, and no file segment
// has, is errNotInCore.
func (c *coreDump) read(addr, size uint64) ([]byte, error) {
	data := make([]byte, 0, size)

	for uint64(len(data)) < size {
		chunk, err := c.chunk(addr+uint64(len(data)), size-uint64(len(data)))
		if err != nil {
			return nil, err
		}

		data = append(data, chunk...)
	}

	return data, nil
}

// chunk returns the memory of the process from addr to the end of the
// segment that holds it, at most size bytes: of the segment of the core,
// or, where the core holds no content, of the file segment, up to the
// next segment of the core.
func (c *coreDump) chunk(addr, size uint64) ([]byte, error) {
	segment, ok := c.fileAt(addr)
	if ok {
		size = min(size, c.nextLoad(addr)-addr)
	}

	prog := c.loadAt(addr)
	if prog != nil {
		segment, ok = fileSegment{vaddr: prog.Vaddr, size: prog.Filesz, data: prog}, true
	}

	if !ok {
		return nil, fmt.Errorf("%w: %#x", errNotInCore, addr)
	}

	data := make([]byte, min(size, segment.vaddr+segment.size-addr))

	//nolint:gosec // An offset within the segment.
	_, err := segment.data.ReadAt(data, int64(addr-segment.vaddr))
	if err != nil {
		return nil, fmt.Errorf("cannot read the memory of the process at %#x: %w", addr, err)
	}

	return data, nil
}

// nextLoad returns the address of the first segment of memory with content
// above addr, or the end of the address space.
func (c *coreDump) nextLoad(addr uint64) uint64 {
	index := sort.Search(len(c.loads), func(i int) bool { return c.loads[i].Vaddr > addr })
	if index == len(c.loads) {
		return math.MaxUint64
	}

	return c.loads[index].Vaddr
}

// fileAt returns the file segment that holds addr.
func (c *coreDump) fileAt(addr uint64) (fileSegment, bool) {
	for _, segment := range c.files {
		if segment.vaddr <= addr && addr-segment.vaddr < segment.size {
			return segment, true
		}
	}

	return fileSegment{vaddr: 0, size: 0, data: nil}, false
}

// withBinary returns c with the memory it holds no content of read, where
// it can be, from binary loaded at bias, as gdb reads it from the
// executable: from the segments of the file the process cannot write to,
// whose content is that of the file. A core leaves out the pages of a file
// mapping a process did not write to, and its coredump_filter (see core(5))
// may leave out the first one of the executable too, where glibc and musl
// point the name of the dynamic loader, the interpreter of the executable.
// The view shares the file of c, which c closes.
func (c *coreDump) withBinary(binary *elf.File, bias uint64) *coreDump {
	view := *c

	view.files = nil

	for _, prog := range binary.Progs {
		if prog.Type == elf.PT_LOAD && prog.Flags&elf.PF_W == 0 && prog.Filesz > 0 {
			view.files = append(view.files, fileSegment{
				vaddr: bias + prog.Vaddr, size: prog.Filesz, data: prog,
			})
		}
	}

	return &view
}

// mappingAt returns the file mapping that holds addr.
func (c *coreDump) mappingAt(addr uint64) (fileMapping, bool) {
	for _, mapping := range c.mappings {
		if mapping.start <= addr && addr < mapping.end {
			return mapping, true
		}
	}

	return fileMapping{start: 0, end: 0, offset: 0, path: ""}, false
}

// executableImage returns the ELF image of the executable of the process,
// read from the memory the core holds: the file mapped from its first byte
// where the program headers of the process are, at AT_PHDR of the
// auxiliary vector. Linux dumps the first page of every ELF file mapped,
// which has the headers, unless its coredump_filter (see core(5)) says
// otherwise, and gcore of gdb does as Linux.
func (c *coreDump) executableImage() (elfImage, error) {
	mapping, ok := c.executable()
	if !ok || mapping.offset != 0 {
		return elfImage{bias: 0, entry: 0, progs: nil}, fmt.Errorf(
			"%w: no file of the core is mapped from its first byte at AT_PHDR", errNoImage)
	}

	return c.imageAt(mapping.start)
}

// executable returns the mapping of the executable of the process: the
// file mapped where its program headers are, AT_PHDR.
func (c *coreDump) executable() (fileMapping, bool) {
	phdr, ok := c.auxv[atPhdr]
	if !ok {
		return fileMapping{start: 0, end: 0, offset: 0, path: ""}, false
	}

	return c.mappingAt(phdr)
}

// binaryImage returns the image of binary as the process of the core
// mapped it, by the headers of the file: for a core that holds no page of
// its executable. An executable that is not position-independent is mapped
// at the addresses of its headers. The bias of another one is that of
// AT_PHDR to the address PT_PHDR of the binary gives, as gdb relocates the
// binary, unless the kernel loaded no interpreter for a binary that has
// one: it ran the interpreter, ld.so BINARY, and AT_PHDR is the loader's.
func (c *coreDump) binaryImage(binary *elf.File) (elfImage, error) {
	none := elfImage{bias: 0, entry: 0, progs: nil}

	progs := make([]elf.ProgHeader, 0, len(binary.Progs))
	for _, prog := range binary.Progs {
		progs = append(progs, prog.ProgHeader)
	}

	if binary.Type == elf.ET_EXEC {
		return elfImage{bias: 0, entry: binary.Entry, progs: progs}, nil
	}

	base, ok := c.auxv[atBase]
	if ok && base == 0 && hasProg(progs, elf.PT_INTERP) {
		return none, fmt.Errorf("%w: the core is of the dynamic loader that ran the binary, "+
			"and holds no page of the binary", errNoImage)
	}

	phdr, ok := c.auxv[atPhdr]

	for _, prog := range binary.Progs {
		if ok && prog.Type == elf.PT_PHDR {
			return elfImage{bias: phdr - prog.Vaddr, entry: binary.Entry, progs: progs}, nil
		}
	}

	return none, fmt.Errorf("%w: no AT_PHDR in the core or no PT_PHDR in the binary", errNoImage)
}

// hasProg reports whether progs have a program header of typ.
func hasProg(progs []elf.ProgHeader, typ elf.ProgType) bool {
	return slices.ContainsFunc(progs, func(prog elf.ProgHeader) bool { return prog.Type == typ })
}

// linkMap returns the link map of the dynamic loader of the process, the
// list of the objects it loaded that gdb's info shared gives: the shared
// libraries its loader loaded and those dlopen(3) did, by the names the
// loader opened them by, then the loader itself, by the interpreter of the
// executable. The loader sets the DT_DEBUG entry of the dynamic section of
// the executable to its r_debug structure, which heads the list of
// link_map structures (see <link.h>). The first of them, with no previous
// one, is the executable, which is left out by its place, as gdb leaves it
// out, and its name is not read: glibc names it "", and musl by
// AT_EXECFN, the path of a position-independent tarantool, and when the
// loader runs it as ld.so PROGRAM, glibc 2.43 points its name into the
// read-only data of the loader, which a core does not hold. An object
// whose name cannot be read is listed with nameErr, and the rest of the
// map is read. A process whose executable has no dynamic section or no
// DT_DEBUG, as a static one, has no link map. The objects of the other
// namespaces of dlmopen(3) are not listed.
func (c *coreDump) linkMap(image elfImage) ([]linkMapEntry, error) {
	debug, err := c.debugEntry(image)
	if err != nil || debug == 0 {
		return nil, err
	}

	// struct r_debug { int r_version; struct link_map *r_map; ... }: the
	// pointer follows the int aligned to a word.
	word := c.wordSize()

	data, err := c.read(debug+word, word)
	if err != nil {
		return nil, fmt.Errorf("cannot read r_debug: %w", err)
	}

	var objects []linkMapEntry

	seen := map[uint64]bool{}

	// struct link_map { ElfW(Addr) l_addr; char *l_name; ElfW(Dyn) *l_ld;
	// struct link_map *l_next, *l_prev; }.
	for entry := c.word(data); entry != 0; entry = c.word(data[3*word:]) {
		switch {
		case seen[entry]:
			return nil, fmt.Errorf("%w at %#x", errLinkMapLoop, entry)
		case len(seen) >= maxLinkMap:
			return nil, fmt.Errorf("%w: more than %d objects", errLinkMapLong, maxLinkMap)
		default:
		}

		first := len(seen) == 0

		seen[entry] = true

		data, err = c.read(entry, 5*word) //nolint:mnd // The five words of a link_map read.
		if err != nil {
			return nil, fmt.Errorf("cannot read link_map: %w", err)
		}

		if first && c.word(data[4*word:]) == 0 {
			continue
		}

		name, nameErr := c.readString(c.word(data[word:]))

		objects = append(objects, linkMapEntry{
			name: name, nameErr: nameErr, addr: c.word(data), dynamic: c.word(data[2*word:]),
		})
	}

	return objects, nil
}

// debugEntry returns the value of the DT_DEBUG entry of the dynamic section
// of image, 0 when image has neither.
func (c *coreDump) debugEntry(image elfImage) (uint64, error) {
	for _, prog := range image.progs {
		if prog.Type != elf.PT_DYNAMIC {
			continue
		}

		data, err := c.read(image.bias+prog.Vaddr, min(prog.Memsz, maxDynamicSize))
		if err != nil {
			return 0, fmt.Errorf("cannot read the dynamic section of the executable: %w", err)
		}

		// ElfW(Dyn) { ElfW(Sxword) d_tag; ElfW(Xword) d_val; }.
		word := c.wordSize()
		for uint64(len(data)) >= 2*word {
			switch elf.DynTag(c.word(data)) { //nolint:gosec // A tag of ELF.
			case elf.DT_NULL:
				return 0, nil
			case elf.DT_DEBUG:
				return c.word(data[word:]), nil
			default:
			}

			data = data[2*word:]
		}
	}

	return 0, nil
}

// readString returns the string that ends with a NUL at addr, empty for
// the address 0.
func (c *coreDump) readString(addr uint64) (string, error) {
	if addr == 0 {
		return "", nil
	}

	var data []byte

	for uint64(len(data)) < maxPathSize {
		chunk, err := c.chunk(addr+uint64(len(data)), maxPathSize-uint64(len(data)))
		if err != nil {
			return "", err
		}

		before, _, found := bytes.Cut(chunk, []byte{0})
		if found {
			return string(append(data, before...)), nil
		}

		data = append(data, chunk...)
	}

	return "", fmt.Errorf("%w: no NUL in %d bytes at %#x", errLongString, maxPathSize, addr)
}

// imageAt reads the ELF image whose first byte is at start: its header and
// its program headers, and the bias of the segment mapped from the first
// byte of the file.
func (c *coreDump) imageAt(start uint64) (elfImage, error) {
	none := elfImage{bias: 0, entry: 0, progs: nil}

	ident, err := c.read(start, elf.EI_NIDENT)
	if err != nil {
		return none, err
	}

	if !bytes.HasPrefix(ident, []byte(elf.ELFMAG)) ||
		elf.Class(ident[elf.EI_CLASS]) != c.file.Class {
		return none, fmt.Errorf("%w at %#x", errNoImage, start)
	}

	var (
		entry, phoff       uint64
		phentsize, phnum   uint16
		headerSize, prSize int
	)

	if c.file.Class == elf.ELFCLASS32 {
		var header elf.Header32

		headerSize, prSize = binary.Size(header), binary.Size(elf.Prog32{})

		err = c.decode(start, headerSize, &header)
		entry, phoff = uint64(header.Entry), uint64(header.Phoff)
		phentsize, phnum = header.Phentsize, header.Phnum
	} else {
		var header elf.Header64

		headerSize, prSize = binary.Size(header), binary.Size(elf.Prog64{})

		err = c.decode(start, headerSize, &header)
		entry, phoff = header.Entry, header.Phoff
		phentsize, phnum = header.Phentsize, header.Phnum
	}

	if err != nil {
		return none, err
	}

	if int(phentsize) < prSize || phnum == 0 {
		return none, fmt.Errorf("%w at %#x: %d program headers of %d bytes", errNoImage, start,
			phnum, phentsize)
	}

	progs, err := c.progs(start+phoff, phnum, uint64(phentsize))
	if err != nil {
		return none, err
	}

	for _, prog := range progs {
		if prog.Type == elf.PT_LOAD && prog.Off == 0 {
			return elfImage{bias: start - prog.Vaddr, entry: entry, progs: progs}, nil
		}
	}

	return none, fmt.Errorf("%w at %#x: no segment maps the first byte of the file", errNoImage,
		start)
}

// decode reads size bytes of memory at addr into the structure out.
func (c *coreDump) decode(addr uint64, size int, out any) error {
	data, err := c.read(addr, uint64(size)) //nolint:gosec // The size of a structure.
	if err != nil {
		return err
	}

	err = binary.Read(bytes.NewReader(data), c.file.ByteOrder, out)
	if err != nil {
		return fmt.Errorf("cannot decode the core dump at %#x: %w", addr, err)
	}

	return nil
}

// progs reads count program headers of entSize bytes each at addr.
func (c *coreDump) progs(addr uint64, count uint16, entSize uint64) ([]elf.ProgHeader, error) {
	progs := make([]elf.ProgHeader, 0, count)

	for index := range uint64(count) {
		header := addr + index*entSize

		if c.file.Class == elf.ELFCLASS32 {
			var prog elf.Prog32

			err := c.decode(header, binary.Size(prog), &prog)
			if err != nil {
				return nil, err
			}

			progs = append(progs, elf.ProgHeader{
				Type: elf.ProgType(prog.Type), Flags: elf.ProgFlag(prog.Flags),
				Off: uint64(prog.Off), Vaddr: uint64(prog.Vaddr), Paddr: uint64(prog.Paddr),
				Filesz: uint64(prog.Filesz), Memsz: uint64(prog.Memsz), Align: uint64(prog.Align),
			})

			continue
		}

		var prog elf.Prog64

		err := c.decode(header, binary.Size(prog), &prog)
		if err != nil {
			return nil, err
		}

		progs = append(progs, elf.ProgHeader{
			Type: elf.ProgType(prog.Type), Flags: elf.ProgFlag(prog.Flags), Off: prog.Off,
			Vaddr: prog.Vaddr, Paddr: prog.Paddr, Filesz: prog.Filesz, Memsz: prog.Memsz,
			Align: prog.Align,
		})
	}

	return progs, nil
}

// imageBuildID returns the GNU build ID of image in hex, read from its note
// segments in the memory of the core: empty when it has none, or the core
// holds none of them.
func (c *coreDump) imageBuildID(image elfImage) string {
	for _, prog := range image.progs {
		if prog.Type != elf.PT_NOTE || prog.Filesz > maxNoteSize {
			continue
		}

		data, err := c.read(image.bias+prog.Vaddr, prog.Filesz)
		if err != nil {
			continue
		}

		id := noteBuildID(data, c.file.ByteOrder, prog.Align)
		if id != "" {
			return id
		}
	}

	return ""
}

// objectBuildID returns the GNU build ID of the image of object, an object
// of the link map, in the memory of the core, or "" when the core holds
// none. The image is that of the file mapped at the dynamic section of the
// object, from its mapping from the first byte of the file below the
// section, or, with no file mapped there, the image at l_addr, where a
// shared library linked at 0 has its first byte.
func (c *coreDump) objectBuildID(object linkMapEntry) string {
	start := object.addr

	mapping, ok := c.mappingAt(object.dynamic)
	if ok {
		for _, other := range c.mappings {
			if other.start > object.dynamic {
				break
			}

			if other.path == mapping.path && other.offset == 0 {
				start = other.start
			}
		}
	}

	image, err := c.imageAt(start)
	if err != nil {
		return ""
	}

	return c.imageBuildID(image)
}
