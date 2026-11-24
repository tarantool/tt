package coredump

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// tarantoolSignature is the symbol a Tarantool executable defines in its
// .text, which tells it from another executable.
const tarantoolSignature = "tarantool_version"

// hostDebugDir is where a debuginfo package installs the separate debugging
// information of an executable, by its build ID: the debug directory gdb
// looks into by default.
const hostDebugDir = "/usr/lib/debug"

// defaultDebugDirs are the debug directories of the host pack looks the
// debug file of the tarantool up in, the first first.
var defaultDebugDirs = []string{hostDebugDir}

// threadDBName is the name of libthread_db, the library gdb loads to read
// the threads of a process.
const threadDBName = "libthread_db.so.1"

// deletedSuffix ends the path Linux records of a mapped file that was
// removed since it was mapped.
const deletedSuffix = " (deleted)"

var (
	// errLibraryGone reports a library of a core that is not on the host as
	// the process loaded it.
	errLibraryGone = errors.New("the file the process loaded is not on the host")
	// errLibraryChanged reports a library of a core whose path names
	// another file than the one the process loaded.
	errLibraryChanged = errors.New("the file at its path is not the one the process loaded")
	// errNotSharedObject reports a file that is no regular ELF shared
	// object.
	errNotSharedObject = errors.New("not an ELF shared object")
)

// noThreadDB is the notice of pack when it finds no libthread_db.
const noThreadDB = `Failed to find libthread_db.so. Brace yourselves, thread debugging
will be a challenging exercise.

This is just a notice, not an error.
`

// multiarchTuples are the multiarch tuples of Debian by the machine of a
// core: the directories of the libraries of the machine under /lib and
// /usr/lib. A machine of more than one tuple has that of the port Debian
// and Ubuntu release, armhf of 32-bit ARM and ppc64el of 64-bit POWER; a
// directory a host lacks is passed over.
var multiarchTuples = map[elf.Machine]string{
	elf.EM_X86_64:  "x86_64-linux-gnu",
	elf.EM_AARCH64: "aarch64-linux-gnu",
	elf.EM_386:     "i386-linux-gnu",
	elf.EM_ARM:     "arm-linux-gnueabihf",
	elf.EM_PPC64:   "powerpc64le-linux-gnu",
	elf.EM_S390:    "s390x-linux-gnu",
	elf.EM_RISCV:   "riscv64-linux-gnu",
}

var (
	// errNoTarantool reports that no tarantool is given to pack and none of
	// those it tries, of PATH among them, is a Tarantool executable.
	errNoTarantool = errors.New("there is no Tarantool executable found in PATH")
	// errNotCore reports a core that is no core dump.
	errNotCore = errors.New("not a core dump")
	// errNotELF reports a tarantool that is no ELF executable.
	errNotELF = errors.New("not an ELF file")
	// errNotTarantool reports an executable that is no Tarantool.
	errNotTarantool = errors.New("not a Tarantool binary")
	// errCoreMismatch reports a core dumped by another executable than the
	// tarantool given, or than every Tarantool executable pack tries for
	// it.
	errCoreMismatch = errors.New(
		"core dump file is produced by the different Tarantool executable")
)

// packArtefacts are what pack collects of the host for a core besides the
// scripts: the tarantool and its version, and the files gdb needs for the
// core.
type packArtefacts struct {
	// executable is the tarantool that dumped the core.
	executable string
	// files are the files of the host to pack, in the order they are
	// packed, as the -f of the pack script takes them: the separate
	// debugging information of the tarantool, NAME=PATH with NAME its
	// member under usr/lib/debug, then the shared libraries of the process
	// and libthread_db, by their paths, which are their members.
	files []string
	// stacks is what the core tells of the stacks of its threads, read
	// while it is open for the rest (see coreDump.threadStacks).
	stacks threadStacks
	// executableType is the type of the ELF file of the tarantool: ET_EXEC
	// of a static build of Tarantool, ET_DYN of a PIE.
	executableType elf.Type
	// buildID is the GNU build ID of the tarantool, empty without one.
	buildID string
	// debugFile is the debug file of the build ID found in a debug
	// directory, empty when none has it.
	debugFile string
	// debugInfo reports whether the tarantool or its debug file has
	// debugging information, as gdb needs for the extensions.
	debugInfo bool
	// choice is how pack found the tarantool of a core it was given none
	// for, which Pack logs; nil with a tarantool given.
	choice *tarantoolChoice
	// pid is the PID of the process of the core, which names the archive
	// of a core given with no PID; 0 when the core has none (see
	// coreDump.pid).
	pid uint
	// version is the version of the tarantool, as the first line of
	// tarantool --version prints it, read from its file (see
	// readBinaryVersion); empty when it cannot be, and versionErr says why.
	version    string
	versionErr error
}

// scriptArgs returns the options of the pack script that pass the files.
func (a packArtefacts) scriptArgs() []string {
	args := make([]string, 0, 2*len(a.files)) //nolint:mnd // An option and a file.
	for _, file := range a.files {
		args = append(args, "-f", file)
	}

	return args
}

// probeArtefacts checks the core and the tarantool of opts before they are
// packed, and finds what to pack with them, where the pack script asked
// gdb, so that a host without gdb packs as one with it does. The checks
// are that the core is a core dump, that the tarantool is an ELF
// executable of Tarantool, and that it is the one that dumped the core.
// The tarantool is the one of opts, or the one findTarantool finds for the
// core. The notices of what is not found go to notices.
func probeArtefacts(notices io.Writer, opts PackOptions, debugDirs []string) (
	packArtefacts, error,
) {
	var none packArtefacts

	core, err := openCore(opts.Core)
	if err != nil {
		return none, fmt.Errorf("%w: %s: the given COREDUMP file is not a valid core dump (see "+
			"core(5)) or not even an ELF file (see elf(5)): %w", errNotCore, opts.Core, err)
	}

	defer func() { _ = core.Close() }()

	if core.fileNoteErr != nil {
		_, _ = fmt.Fprintf(notices, "Cannot read the files the core maps from its NT_FILE note: "+
			"%v. The shared libraries are packed by the link map of the core alone.\n",
			core.fileNoteErr)
	}

	var (
		executable = opts.Executable
		binary     *elf.File
		choice     *tarantoolChoice
	)

	if executable != "" {
		binary, err = checkTarantool(core, opts.Core, executable)
	} else {
		binary, choice, err = findTarantool(core, opts)
	}

	if err != nil {
		return none, err
	}

	defer func() { _ = binary.Close() }()

	if choice != nil {
		executable = choice.taken.Path
	}

	var files []string

	// The debug file goes to usr/lib/debug of the archive, where gdb.sh
	// points gdb to, whatever debug directory of the host has it.
	build := buildID(binary)

	debugFile := findDebugFile(debugDirs, build)
	if debugFile != "" {
		files = append(files, debugFilePath(debugFileDir, build)+"="+debugFile)
	}

	libs := sharedLibraries(notices, core, loadedObjects(notices, core, binary), executable)

	files = append(files, libs...)

	threadDB := findThreadDB(libs, systemLibraryDirs(core.file.Machine))
	if threadDB == "" {
		_, _ = io.WriteString(notices, noThreadDB)
	} else if !slices.Contains(files, threadDB) {
		files = append(files, threadDB)
	}

	pid, _ := core.pid()

	version, versionErr := readBinaryVersion(binary)

	return packArtefacts{
		executable: executable, files: files, stacks: core.threadStacks(),
		executableType: binary.Type, buildID: build, debugFile: debugFile,
		debugInfo: hasDWARF(binary) || fileHasDWARF(debugFile), choice: choice, pid: pid,
		version: version, versionErr: versionErr,
	}, nil
}

// checkTarantool opens executable, the tarantool given to pack the core at
// corePath, open as core, with: it is to be an ELF executable of Tarantool
// that dumped the core. Another one is an error that says what is wrong.
func checkTarantool(core *coreDump, corePath, executable string) (*elf.File, error) {
	binary, err := openExecutable(executable)
	if err != nil {
		return nil, err
	}

	if !hasSignature(binary) {
		_ = binary.Close()

		return nil, fmt.Errorf("%w: %s: there is no signature symbol, %s, in the binary file; "+
			"check the BINARY file with `%s --help`", errNotTarantool, executable,
			tarantoolSignature, executable)
	}

	mismatch := coreMismatch(core, binary)
	if mismatch != "" {
		_ = binary.Close()

		return nil, fmt.Errorf("%w: looks like '%s' is not generated by `%s': %s; check that "+
			"the given COREDUMP is produced by the specified BINARY", errCoreMismatch,
			corePath, executable, mismatch)
	}

	return binary, nil
}

// fileHasDWARF reports whether the ELF file at path carries debugging
// information (see hasDWARF); a path that is empty or names no ELF file
// has none.
func fileHasDWARF(path string) bool {
	if path == "" {
		return false
	}

	file, err := elf.Open(path)
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	return hasDWARF(file)
}

// warnPackDebugInfo warns, before the archive is made, when neither the
// tarantool of artefacts nor a debug file of its build ID in debugDirs has
// debugging information: the archive will have none, and the extensions
// will not work on the core, while the debug file may still be installed
// and the core packed again, or, of a static build of Community, the core
// packed with the tarantool of the static deb, which has it (see
// staticDebRelease), as the hint says.
func warnPackDebugInfo(log *slog.Logger, artefacts packArtefacts, debugDirs []string) {
	if artefacts.debugInfo {
		return
	}

	var missing string

	switch {
	case artefacts.buildID == "":
		missing = "it has no build ID to find a debug file by"
	case artefacts.debugFile != "":
		missing = "neither has the debug file " + artefacts.debugFile + " of its build ID"
	default:
		missing = fmt.Sprintf("no debug file of its build ID %s is under %s", artefacts.buildID,
			strings.Join(debugDirs, ", "))
	}

	hint := "install the debug file of the tarantool, of the debuginfo or dbgsym package of " +
		"its package, and pack the core again"

	release, ok := staticDebRelease(artefacts.version, artefacts.executableType,
		artefacts.buildID)
	if ok {
		hint = staticDebHint(release, artefacts.buildID,
			"pack the core with it, `tt coredump pack -e <its path>`")
	}

	warnWithHint(log, fmt.Sprintf("the tarantool %s has no debugging information, and %s: the "+
		"archive will have none, and the lj-* commands of LuaJIT and the pretty-printers of "+
		"Tarantool will not work on the core, plain backtraces will", artefacts.executable,
		missing), hint)
}

// warnPackVersion warns, before the archive is made, when the version of
// the tarantool of artefacts cannot be read from its file: the archive
// says it is unknown, and inspect can do nothing of what it does by it.
func warnPackVersion(log *slog.Logger, artefacts packArtefacts) {
	if artefacts.versionErr == nil {
		return
	}

	log.Warn(fmt.Sprintf("cannot read the version of the tarantool %s from its file: %v: the "+
		"archive will say it is unknown, and tt coredump inspect will load the gdb extensions "+
		"of tt, which may not match the core, rather than those of its version, and will not "+
		"say which commit of the sources of Tarantool to check out", artefacts.executable,
		artefacts.versionErr))
}

// versionMember is the member of the archive with the version of the
// tarantool.
const versionMember = "version"

// versionFileMode is the mode of the version file pack writes for the pack
// script.
const versionFileMode = 0o600

// stageVersion writes the version of artefacts into dir and returns the -f
// arguments of the pack script that put it into the archive as its
// version. A version that cannot be read, empty, is no file: the script
// writes a version that says it is unknown then.
func stageVersion(dir string, artefacts packArtefacts) ([]string, error) {
	if artefacts.version == "" {
		return nil, nil
	}

	path := filepath.Join(dir, versionMember)

	err := os.WriteFile(path, []byte(artefacts.version+"\n"), versionFileMode)
	if err != nil {
		return nil, fmt.Errorf("cannot stage the version of the tarantool: %w", err)
	}

	return []string{"-f", versionMember + "=" + path}, nil
}

// loadedObjects returns the link map of core, the objects its dynamic
// loader loaded, through the image of its program in the core, as
// programImage finds it, or, when the core holds none, through binary. The
// memory the core holds no content of is read from the file of binary
// where it can be, as gdb reads it. A link map that cannot be read is a
// notice on notices, and none; a static binary, with no dynamic section,
// has none to read. An object whose name cannot be read is named as
// unreadName says, with a notice, and left out when it cannot be named.
func loadedObjects(notices io.Writer, core *coreDump, binary *elf.File) []linkMapEntry {
	image, err := programImage(core, binary)
	if err != nil {
		if !slices.ContainsFunc(binary.Progs, func(prog *elf.Prog) bool {
			return prog.Type == elf.PT_DYNAMIC
		}) {
			return nil
		}

		image, err = core.binaryImage(binary)
	}

	var objects []linkMapEntry

	if err == nil {
		objects, err = core.withBinary(binary, image.bias).linkMap(image)
	}

	if err != nil {
		_, _ = fmt.Fprintf(notices, "Cannot read the link map of the dynamic loader in the core: "+
			"%v. The shared libraries are packed by the paths the core records of the files it "+
			"maps, which gdb may not look them up by.\n", err)
	}

	named := make([]linkMapEntry, 0, len(objects))

	for _, object := range objects {
		if object.nameErr != nil {
			object.name = unreadName(core, binary, object)

			if object.name == "" {
				_, _ = fmt.Fprintf(notices, "Cannot read the name of the shared library loaded at "+
					"%#x in the core: %v. It is not packed: the core maps no file there.\n",
					object.addr, object.nameErr)

				continue
			}

			_, _ = fmt.Fprintf(notices, "Cannot read the name of the shared library loaded at %#x "+
				"in the core: %v. It is packed as %s, which gdb may not look it up by.\n",
				object.addr, object.nameErr, object.name)
		}

		named = append(named, object)
	}

	return named
}

// unreadName returns the name to pack object of the link map of core by
// when its name cannot be read: the interpreter of binary for the dynamic
// loader, the object loaded where the kernel loaded the interpreter of the
// executable, AT_BASE, as glibc and musl name the loader; the path the
// core records of the file mapped at its dynamic section for another one;
// "" when the core maps no file there.
func unreadName(core *coreDump, binary *elf.File, object linkMapEntry) string {
	base := core.auxv[atBase]
	if base != 0 && object.addr == base {
		interp := interpreter(binary)
		if interp != "" {
			return interp
		}
	}

	mapping, ok := core.mappingAt(object.dynamic)
	if !ok {
		return ""
	}

	return strings.TrimSuffix(mapping.path, deletedSuffix)
}

// interpreter returns the interpreter binary names, PT_INTERP, or "".
func interpreter(binary *elf.File) string {
	for _, prog := range binary.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}

		data, err := io.ReadAll(io.LimitReader(prog.Open(), maxPathSize))
		if err != nil {
			return ""
		}

		before, _, _ := bytes.Cut(data, []byte{0})

		return string(before)
	}

	return ""
}

// sharedLibraries returns the shared libraries of the process of core to
// pack, by their paths. The objects of the link map of its dynamic loader,
// objects, come first, by the names the loader opened them by: gdb lists
// them so with info shared, which pack used to take, and looks them up so
// in the archive. Then come the other ELF shared objects the process had
// mapped, by the paths of the NT_FILE note, which are the real paths of
// the files: one of another namespace of dlmopen(3), or a library whose
// link map is unreadable. A mapping of a file deleted since, of a device,
// of a memfd, of the executable or of a file packed already under another
// name is left out. A path with a newline, which the pack script cannot
// take, is too.
//
// A library of the link map that is no longer on the host is left out
// with a notice on notices: its file is deleted, as Tarantool deletes the
// copy of the module of a C function of box.func once it has loaded it,
// or another file is at its path now, as loadedLibraryInfo tells. gdb has
// no symbols of it, packed or not; the symbolic link the pack script
// staged for a deleted one dangled, and tar failed on it.
func sharedLibraries(notices io.Writer, core *coreDump, objects []linkMapEntry,
	binary string,
) []string {
	var (
		libs   []string
		packed []os.FileInfo
	)

	named := map[string]bool{}

	for _, object := range objects {
		if !packablePath(object.name) || named[object.name] {
			continue
		}

		named[object.name] = true

		info, err := loadedLibraryInfo(core, object)
		if err != nil {
			_, _ = fmt.Fprintf(notices, "The shared library %s of the core is not packed: %v. "+
				"gdb will have no symbols of it.\n", object.name, err)

			continue
		}

		libs = append(libs, object.name)
		packed = append(packed, info)
	}

	info, err := os.Stat(binary)
	if err == nil {
		packed = append(packed, info)
	}

	exe, _ := core.executable()

	for _, mapping := range core.mappings {
		if mapping.path == exe.path || named[mapping.path] || !packableMapping(mapping.path) {
			continue
		}

		named[mapping.path] = true

		info, err := sharedObjectInfo(mapping.path)
		if err != nil || slices.ContainsFunc(packed, func(other os.FileInfo) bool {
			return os.SameFile(info, other)
		}) {
			continue
		}

		libs = append(libs, mapping.path)
		packed = append(packed, info)
	}

	return libs
}

// loadedLibraryInfo returns the information of the file of object, a
// library of the link map of core, when it is the one the process loaded:
// its path names a regular ELF shared object, and its build ID is that of
// the image of the object in the core, whose first page Linux and gcore
// dump. A file of the build ID is the library even when the mapping at
// the dynamic section of the object says its file was deleted, as a
// package reinstalled with the same content leaves it; a file of another
// one is not, as a link re-pointed to a new version is not, whatever the
// mapping says. Without a build ID on a side, the mapping is to be of a
// file not deleted. A file pack cannot read is an error that says why.
func loadedLibraryInfo(core *coreDump, object linkMapEntry) (os.FileInfo, error) {
	mapping, ok := core.mappingAt(object.dynamic)
	deleted := ok && strings.HasSuffix(mapping.path, deletedSuffix)

	info, err := os.Stat(object.name)
	if err != nil && deleted {
		return nil, fmt.Errorf("%w: %s", errLibraryGone, mapping.path)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w", errLibraryGone, err)
	}

	_, err = sharedObjectInfo(object.name)
	if errors.Is(err, errNotSharedObject) {
		return nil, fmt.Errorf("%w: %w", errLibraryGone, errNotSharedObject)
	}

	if err != nil {
		return nil, err
	}

	loaded, onDisk := core.objectBuildID(object), fileBuildID(object.name)

	switch {
	case loaded != "" && onDisk != "" && loaded != onDisk:
		return nil, fmt.Errorf("%w: it has build ID %s, the process loaded %s",
			errLibraryChanged, onDisk, loaded)
	case loaded != "" && onDisk != "":
		return info, nil
	case deleted:
		return nil, fmt.Errorf("%w: %s", errLibraryGone, mapping.path)
	default:
		return info, nil
	}
}

// fileBuildID returns the GNU build ID of the ELF file at path, or "".
func fileBuildID(path string) string {
	file, err := elf.Open(path)
	if err != nil {
		return ""
	}

	defer func() { _ = file.Close() }()

	return buildID(file)
}

// packablePath reports whether path is one the pack script takes: an
// absolute one without a newline.
func packablePath(path string) bool {
	return filepath.IsAbs(path) && !strings.ContainsRune(path, '\n')
}

// packableMapping reports whether path, the path of a mapping of NT_FILE,
// may be a file of the host to pack: not of a file deleted since it was
// mapped, which Linux marks so, a device, as /dev/zero of a shared
// anonymous mapping, or a memfd.
func packableMapping(path string) bool {
	return packablePath(path) && !strings.HasSuffix(path, deletedSuffix) &&
		!strings.HasPrefix(path, "/dev/") && !strings.HasPrefix(path, "/memfd:")
}

// sharedObjectInfo returns the information of the file at path when it is
// a regular ELF shared object. Another file is errNotSharedObject, and a
// file that cannot be read the error of reading it.
func sharedObjectInfo(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read it: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, errNotSharedObject
	}

	file, err := elf.Open(path)
	if err != nil {
		// The system refused to open or read the file, or it is no ELF file:
		// short, or of another format.
		_, unreadable := errors.AsType[*fs.PathError](err)
		if unreadable {
			return nil, fmt.Errorf("cannot read it: %w", err)
		}

		return nil, errNotSharedObject
	}

	defer func() { _ = file.Close() }()

	if file.Type != elf.ET_DYN {
		return nil, errNotSharedObject
	}

	return info, nil
}

// findThreadDB returns the libthread_db gdb loads for a process of the
// shared libraries libs, or "" when there is none: gdb looks it up by its
// default libthread-db-search-path, $pdir:$sdir. $pdir is the directory
// of the thread library of the process, the first of libs named libpthread
// or libc that defines pthread_create: glibc 2.34 merged libpthread into
// libc, and keeps an empty libpthread. $sdir is where the dynamic loader
// of the host finds the library, the systemDirs.
func findThreadDB(libs, systemDirs []string) string {
	for _, lib := range libs {
		if !isThreadLibrary(lib) {
			continue
		}

		path := filepath.Join(filepath.Dir(lib), threadDBName)
		if isRegularFile(path) {
			return path
		}

		// gdb looks next to the first thread library alone.
		break
	}

	for _, dir := range systemDirs {
		path := filepath.Join(dir, threadDBName)
		if isRegularFile(path) {
			return path
		}
	}

	return ""
}

// isThreadLibrary reports whether lib is the thread library of a process
// to gdb: a libpthread or a libc, by its path, that defines pthread_create.
func isThreadLibrary(lib string) bool {
	if !strings.Contains(lib, "/libpthread") && !strings.Contains(lib, "/libc.") {
		return false
	}

	file, err := elf.Open(lib)
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	symbols, err := file.DynamicSymbols()
	if err != nil {
		return false
	}

	return slices.ContainsFunc(symbols, func(symbol elf.Symbol) bool {
		return symbol.Name == "pthread_create" && symbol.Section != elf.SHN_UNDEF
	})
}

// systemLibraryDirs returns the directories the dynamic loader of a host of
// machine looks libraries up in by default: those of the multiarch tuple
// of the machine, as Debian has them, then those of the libraries of the
// 64-bit and of any machine.
func systemLibraryDirs(machine elf.Machine) []string {
	var dirs []string

	tuple, ok := multiarchTuples[machine]
	if ok {
		dirs = append(dirs, "/lib/"+tuple, "/usr/lib/"+tuple)
	}

	return append(dirs, "/lib64", "/usr/lib64", "/lib", "/usr/lib")
}

// findDebugFile returns the separate debugging information of the build ID
// build, .build-id/<xx>/<rest>.debug, under the first of the debug
// directories dirs that has it, or "" when none has.
func findDebugFile(dirs []string, build string) string {
	if len(build) < 3 { //nolint:mnd // The directory is named by two digits, the file by the rest.
		return ""
	}

	for _, dir := range dirs {
		path := debugFilePath(dir, build)
		if isRegularFile(path) {
			return path
		}
	}

	return ""
}

// isRegularFile reports whether path is a regular file, or a symbolic link
// to one.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// openExecutable opens the ELF executable at path: an executable or a
// shared object, as a position-independent executable is.
func openExecutable(path string) (*elf.File, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: the given BINARY file is not an ELF file (see elf(5)): "+
			"%w", errNotELF, path, err)
	}

	if file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		_ = file.Close()

		return nil, fmt.Errorf("%w: %s: the given BINARY file is an ELF file of type %s, not "+
			"an executable", errNotELF, path, file.Type)
	}

	return file, nil
}

// hasSignature reports whether file is a Tarantool executable: it defines
// tarantoolSignature in .text, in its symbol table or its dynamic one, as
// `info symbol tarantool_version` of gdb tells. Tarantool exports the
// symbol, so a stripped tarantool keeps it in the dynamic table.
func hasSignature(file *elf.File) bool {
	_, ok := textSymbol(file, tarantoolSignature)

	return ok
}

// coreMismatch returns why the executable of core is not binary, or ""
// when it is, as far as the core tells. Both carry a GNU build ID, as a
// linker writes by default, and the core has that of the image of the
// executable in its memory: the first page of the file, which Linux dumps.
// With no build ID on a side, the entry point and the program headers of
// the image are compared. A core with no image of the executable, as one
// dumped with the ELF headers filtered out of it is, tells nothing, and the
// binary is taken. The executable is the program the dynamic loader ran, as
// programImage finds it.
//
// gdb only warns of such a core, and pack used to refuse it when gdb found
// no shared library of it with the binary: when the dynamic section of the
// binary was not where that of the executable of the core was. A binary of
// the same layout passed, and a static one, with none, failed.
func coreMismatch(core *coreDump, binary *elf.File) string {
	image, err := programImage(core, binary)
	if err != nil {
		return ""
	}

	return imageMismatch(core, image, binary)
}

// programImage returns the image in core of the program its process ran:
// the executable at AT_PHDR, but when it has no interpreter, the image of
// binary with one that the core maps from its first byte, if there is
// one. The kernel ran the dynamic loader with the program as its argument,
// ld.so PROGRAM, and AT_PHDR is that of the loader, which mapped the
// program; a static executable has no interpreter either, and no such
// image.
func programImage(core *coreDump, binary *elf.File) (elfImage, error) {
	image, err := core.executableImage()
	if err != nil || hasProg(image.progs, elf.PT_INTERP) {
		return image, err
	}

	for _, mapping := range core.mappings {
		if mapping.offset != 0 {
			continue
		}

		program, err := core.imageAt(mapping.start)
		if err == nil && hasProg(program.progs, elf.PT_INTERP) &&
			imageMismatch(core, program, binary) == "" {
			return program, nil
		}
	}

	return image, nil
}

// imageMismatch returns why image of core is not binary, or "" when it is,
// as coreMismatch compares them.
func imageMismatch(core *coreDump, image elfImage, binary *elf.File) string {
	coreID, binaryID := core.imageBuildID(image), buildID(binary)
	if coreID != "" && binaryID != "" {
		if coreID != binaryID {
			return fmt.Sprintf("the executable of the core has build ID %s, the binary %s",
				coreID, binaryID)
		}

		return ""
	}

	if !sameLayout(image, binary) {
		return "the entry point or the program headers of the executable of the core differ " +
			"from those of the binary"
	}

	return ""
}

// sameLayout reports whether image has the entry point and the program
// headers of binary.
func sameLayout(image elfImage, binary *elf.File) bool {
	if image.entry != binary.Entry || len(image.progs) != len(binary.Progs) {
		return false
	}

	for i, prog := range binary.Progs {
		if prog.ProgHeader != image.progs[i] {
			return false
		}
	}

	return true
}
