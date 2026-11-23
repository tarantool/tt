package coredump

import (
	"debug/elf"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// threadLocalLua is the first release of Tarantool whose Lua state of the
// main thread, tarantool_L, is a thread-local variable: what tells it of a
// tarantool whose symbols do not (see readLuaState).
var threadLocalLua = release{3, 7, 0}

// luaStateName is the name of the Lua state of the main thread of
// Tarantool.
const luaStateName = "tarantool_L"

// gdbOwnTLS is the first release of gdb that reads the thread-local
// variables of a core without libthread_db, "GDB-internal Thread Local
// Storage (TLS) support" of the NEWS of gdb 17: gdb 16.3 fails with "Cannot
// find thread-local variables on this target" where no libthread_db loads.
// A development version of 17, 17.0.50 of a snapshot or 17.0.90 of a
// pre-release, is before it, and has the support or not by its date.
var gdbOwnTLS = debuggerVersion{17, 1, 0}

// zeroedStacksInstead is what to do instead of a core with stacks of zeros
// (see threadStacks), which libthread_db fails on, as the hint of their
// warning says.
const zeroedStacksInstead = "take the core the kernel writes, or write it with gcore-pages of " +
	"tarantool-gdb-tools"

// luaState is what the symbols of the tarantool of a core tell of
// tarantool_L.
type luaState int

const (
	// luaStateUnlisted is of symbols that list no tarantool_L, as those of
	// a stripped tarantool without a debug file: the version tells.
	luaStateUnlisted luaState = iota
	// luaStateThreadLocal is of a tarantool_L of type STT_TLS.
	luaStateThreadLocal
	// luaStateGlobal is of a tarantool_L of another type, a variable of the
	// process.
	luaStateGlobal
)

// libcName is the name of the C library of glibc, next to which pack puts
// libthread_db and gdb looks for it, $pdir.
const libcName = "libc.so.6"

// errNoGlibcVersion reports a C library whose glibc cannot be told.
var errNoGlibcVersion = errors.New("no version of glibc")

// errNoHostLibrary reports a library this host has none of where its
// dynamic loader looks.
var errNoHostLibrary = errors.New("no such library on this host")

// glibcVersion is a version of glibc, its major and minor.
type glibcVersion [2]int

// String returns v as x.y.
func (v glibcVersion) String() string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1])
}

// known reports whether v is a version, not the zero one of a glibc that
// cannot be told.
func (v glibcVersion) known() bool {
	return v != glibcVersion{}
}

// compare returns a negative number, zero or a positive one as v is before
// other, the same or after it.
func (v glibcVersion) compare(other glibcVersion) int {
	return slices.Compare(v[:], other[:])
}

// glibcBanner matches the version in the banner libc.so.6 of glibc prints
// when it is run, which its .rodata holds: "GNU C Library (Ubuntu GLIBC
// 2.43-2ubuntu2.7) stable release version 2.43.", or "development release
// version 2.43.9000." of a snapshot.
var glibcBanner = regexp.MustCompile(`release version (\d+)\.(\d+)`)

// glibcDefinition matches a version definition of glibc for a release,
// GLIBC_2.43; GLIBC_PRIVATE and GLIBC_ABI_DT_X86_64_PLT are none.
var glibcDefinition = regexp.MustCompile(`^GLIBC_(\d+)\.(\d+)(?:\.\d+)?$`)

// hostMachines are the machines of the libraries of a host by the GOARCH
// of tt, which runs there natively, as gdb does.
var hostMachines = map[string]elf.Machine{
	"amd64":   elf.EM_X86_64,
	"arm64":   elf.EM_AARCH64,
	"386":     elf.EM_386,
	"arm":     elf.EM_ARM,
	"ppc64le": elf.EM_PPC64,
	"s390x":   elf.EM_S390,
	"riscv64": elf.EM_RISCV,
}

// hostLibraries returns the machine of the libraries of this host, none
// when it cannot be told, and the directories its dynamic loader looks
// them up in (see systemLibraryDirs), as pack finds them: gdb runs with
// the libc there, and the libthread_db it loads binds to it. Tests replace
// it.
var hostLibraries = func() (elf.Machine, []string) {
	machine := hostMachines[runtime.GOARCH]

	return machine, systemLibraryDirs(machine)
}

// threadDBFacts is what tells whether gdb reads tarantool_L of a core: the
// Lua state of its main thread, thread-local since Tarantool 3.7, which gdb
// before 17.1 reads only through libthread_db. gdb dlopen()s the
// libthread_db of the archive, next to its libc and of its glibc, into
// itself, where it binds to the libc of this host, which is to define the
// versions it needs; failing that, it loads the one of this host, which
// td_ta_new takes for a core of its own glibc and machine alone. Either
// reads the descriptors of the threads glibc keeps at the top of their
// stacks, and fails on a core that has them as zeros, "Cannot find new
// threads: debugger service failed". The facts that cannot be told say
// why.
type threadDBFacts struct {
	// Release is the release of the Tarantool of the core; zero when it
	// cannot be told.
	Release release
	// LuaState is what the symbols of the tarantool of the core tell of
	// tarantool_L, and LuaStateUnlisted why they tell nothing when they do
	// not.
	LuaState         luaState
	LuaStateUnlisted string
	// GDB is the version of gdb; zero when it cannot be told.
	GDB debuggerVersion
	// ZeroedStacks is the number of the threads of the core besides the
	// main one whose stacks are zeros (see threadStacks).
	ZeroedStacks int
	// ThreadDB reports whether the archive has a libthread_db next to its
	// libc.
	ThreadDB bool
	// NoLoad is why gdb of this host does not load it, empty when it does.
	NoLoad string
	// ThreadDBUnknown is why whether it loads cannot be told, empty when
	// it can.
	ThreadDBUnknown string
	// CoreMachine is the machine of the libc of the archive, that of the
	// core; HostMachine is that of the libraries of this host. Either is
	// EM_NONE when it cannot be told.
	CoreMachine elf.Machine
	HostMachine elf.Machine
	// CoreGlibc is the glibc of the core, of the libc of the archive; zero
	// when it cannot be told, and CoreGlibcUnknown says why.
	CoreGlibc        glibcVersion
	CoreGlibcUnknown string
	// HostGlibc is the glibc of the libc of this host; zero when it cannot
	// be told, and HostGlibcUnknown says why.
	HostGlibc        glibcVersion
	HostGlibcUnknown string
}

// threadLocal reports whether tarantool_L of the core is thread-local: as
// its symbol says, or, of a tarantool whose symbols do not list it, as the
// version of the core does. unknown says what cannot be told when neither
// tells.
func (f threadDBFacts) threadLocal() (bool, string) {
	switch {
	case f.LuaState == luaStateThreadLocal:
		return true, ""
	case f.LuaState == luaStateGlobal:
		return false, ""
	case f.Release == release{}:
		return false, "the version of the core"
	default:
		return f.Release.compare(threadLocalLua) >= 0, ""
	}
}

// otherMachine reports whether the core is of another machine than this
// host, as far as both can be told.
func (f threadDBFacts) otherMachine() bool {
	return f.CoreMachine != elf.EM_NONE && f.HostMachine != elf.EM_NONE &&
		f.CoreMachine != f.HostMachine
}

// developmentOwnTLS reports whether gdb is a development version of the
// release that reads thread-local variables on its own (see gdbOwnTLS),
// which may read them so or not.
func (f threadDBFacts) developmentOwnTLS() bool {
	return f.GDB[0] == gdbOwnTLS[0] && f.GDB.compare(gdbOwnTLS) < 0
}

// tlsProblem returns why gdb will not read tarantool_L of the core, and
// what to do instead but to use a later gdb, or empty strings when it
// will. unknown says what cannot be told when the facts tell neither.
func (f threadDBFacts) tlsProblem() (string, string, string) {
	threadLocal, unknown := f.threadLocal()

	switch {
	case !f.GDB.known():
		return "", "", "the version of gdb"
	case f.GDB.compare(gdbOwnTLS) >= 0:
		return "", "", ""
	case unknown != "":
		return "", "", unknown
	case !threadLocal:
		return "", "", ""
	// No libthread_db reads the threads of such a core, whichever loads.
	case f.ZeroedStacks > 0:
		return "libthread_db fails on the stacks of zeros of this core", zeroedStacksInstead, ""
	case f.ThreadDB && f.ThreadDBUnknown != "":
		return "", "", "whether gdb loads the libthread_db of the archive: " + f.ThreadDBUnknown
	// The libthread_db of the archive is of the glibc of the core.
	case f.ThreadDB && f.NoLoad == "":
		return "", "", ""
	case !f.CoreGlibc.known():
		return "", "", "the glibc of the core: " + f.CoreGlibcUnknown
	}

	// The libthread_db of this host is of its machine, whatever its glibc.
	mismatch := fmt.Sprintf("that of this host is of %s, the core of %s", f.HostMachine,
		f.CoreMachine)
	host := "a host of " + f.CoreMachine.String() + " and glibc " + f.CoreGlibc.String()

	if !f.otherMachine() {
		switch {
		case !f.HostGlibc.known():
			return "", "", "the glibc of this host: " + f.HostGlibcUnknown
		case f.CoreGlibc == f.HostGlibc:
			return "", "", ""
		}

		mismatch = fmt.Sprintf("that of this host is of glibc %s, the core of %s", f.HostGlibc,
			f.CoreGlibc)
		host = "a host of glibc " + f.CoreGlibc.String()
	}

	// A libc of the glibc of the core or later defines every version its
	// libthread_db needs: glibc adds versions and removes none.
	if f.ThreadDB {
		return "the libthread_db of the archive does not load here, " + f.NoLoad + ", and " +
			mismatch, "or inspect the core on " + host + " or later", ""
	}

	return "the archive has no libthread_db, and " + mismatch, "or inspect the core on " + host, ""
}

// libcVersion returns the version of the glibc of the C library at path:
// that of its banner, or, of a libc without one, the highest of its
// version definitions of a release, GLIBC_x.y, which glibc adds for the
// symbols a release adds. A release that adds none, as 2.37 and 2.40 on
// x86_64, has no definition of its own, so that the definitions may tell a
// version lower than it is, never higher.
func libcVersion(path string) (glibcVersion, error) {
	file, err := elf.Open(path)
	if err != nil {
		return glibcVersion{}, fmt.Errorf("cannot read %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	version, ok := bannerVersion(file)
	if ok {
		return version, nil
	}

	// A file without version definitions is an error of debug/elf.
	definitions, err := file.DynamicVersions()
	if err != nil {
		return glibcVersion{}, fmt.Errorf("%w in %s: no banner, and %w", errNoGlibcVersion, path,
			err)
	}

	var highest glibcVersion

	for _, definition := range definitions {
		match := glibcDefinition.FindStringSubmatch(definition.Name)
		if match == nil {
			continue
		}

		version, ok := parseGlibcVersion(match[1], match[2])
		if ok && version.compare(highest) > 0 {
			highest = version
		}
	}

	if !highest.known() {
		return glibcVersion{}, fmt.Errorf("%w in %s: no banner, and no version definition "+
			"GLIBC_x.y", errNoGlibcVersion, path)
	}

	return highest, nil
}

// bannerVersion returns the version of the banner of glibc in .rodata of
// file, a libc, and reports false of a file without one.
func bannerVersion(file *elf.File) (glibcVersion, bool) {
	rodata := file.Section(".rodata")
	if rodata == nil {
		return glibcVersion{}, false
	}

	data, err := rodata.Data()
	if err != nil {
		return glibcVersion{}, false
	}

	match := glibcBanner.FindSubmatch(data)
	if match == nil {
		return glibcVersion{}, false
	}

	return parseGlibcVersion(string(match[1]), string(match[2]))
}

// parseGlibcVersion returns the version of glibc of the numbers major and
// minor, and reports false of a number it cannot read.
func parseGlibcVersion(major, minor string) (glibcVersion, bool) {
	var version glibcVersion

	for part, number := range []string{major, minor} {
		parsed, err := strconv.Atoi(number)
		if err != nil {
			return glibcVersion{}, false
		}

		version[part] = parsed
	}

	return version, true
}

// findLibrary returns the library name of the machine in the first of dirs
// that has it, any machine when it is none, or "".
func findLibrary(dirs []string, name string, machine elf.Machine) string {
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if !isRegularFile(path) {
			continue
		}

		file, err := elf.Open(path)
		if err != nil {
			continue
		}

		found := file.Machine

		_ = file.Close()

		if machine == elf.EM_NONE || found == machine {
			return path
		}
	}

	return ""
}

// definedVersions returns the names of the versions the library at path
// defines.
func definedVersions(path string) (map[string]bool, error) {
	file, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	definitions, err := file.DynamicVersions()
	if err != nil {
		return nil, fmt.Errorf("cannot read the versions %s defines: %w", path, err)
	}

	defined := make(map[string]bool, len(definitions))

	for _, definition := range definitions {
		defined[definition.Name] = true
	}

	return defined, nil
}

// threadDBLoadProblem returns why gdb of a host of machine, whose dynamic
// loader looks libraries up in dirs, does not load the libthread_db at
// path, or "" when it does: gdb dlopen()s it into itself, and the dynamic
// loader refuses a library of another machine, and one that needs a
// version, DT_VERNEED, its library on the host does not define. A weak
// version is not needed. The version numbers of glibc do not tell it: the
// libthread_db of Debian 13, of glibc 2.41, needs versions of 2.36 and
// before, and that of 2.43 needs GLIBC_ABI_DT_X86_64_PLT on x86_64, which
// 2.41 defines and 2.39 does not. What cannot be read is an error.
func threadDBLoadProblem(path string, machine elf.Machine, dirs []string) (string, error) {
	file, err := elf.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	if machine != elf.EM_NONE && file.Machine != machine {
		return fmt.Sprintf("it is a library of %s, this host is of %s", file.Machine, machine), nil
	}

	needs, err := file.DynamicVersionNeeds()
	if err != nil {
		return "", fmt.Errorf("cannot read the versions %s needs: %w", path, err)
	}

	for _, need := range needs {
		lib := findLibrary(dirs, need.Name, file.Machine)
		if lib == "" {
			return "", fmt.Errorf("%w: it needs %s, and none is under %s", errNoHostLibrary,
				need.Name, strings.Join(dirs, ", "))
		}

		defined, err := definedVersions(lib)
		if err != nil {
			return "", err
		}

		for _, dep := range need.Needs {
			if dep.Flags&elf.VER_FLG_WEAK == 0 && !defined[dep.Dep] {
				return fmt.Sprintf("it needs %s, which %s does not define", dep.Dep, lib), nil
			}
		}
	}

	return "", nil
}

// findArchiveLibc returns the libc of the artefacts in dir, the first one
// under its path in the archive, or "" when it has none, as an archive of a
// static tarantool has none. The sources, the extensions and the debug
// files are not looked through.
func findArchiveLibc(dir string) string {
	skipped := []string{"sources", "ext", filepath.Join("usr", "lib", "debug")}
	found := ""

	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // An unreadable directory has no libc to tell.
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr == nil && entry.IsDir() && slices.Contains(skipped, rel) {
			return filepath.SkipDir
		}

		if !entry.IsDir() && entry.Name() == libcName {
			found = path

			return filepath.SkipAll
		}

		return nil
	})

	return found
}

// readLuaState returns what the symbols of the tarantool of the artefacts
// in dir tell of tarantool_L: those of its .symtab, else of its .dynsym,
// else of the .symtab of the debug file of its build ID under usr/lib/debug
// of the artefacts, which gdb.sh points gdb to. A stripped tarantool keeps
// no .symtab, and a static build no .dynsym. Of symbols that list no
// tarantool_L it says why: what was read, and what could not be.
func readLuaState(dir string) (luaState, string) {
	tarantool := filepath.Join(dir, "tarantool")

	file, err := elf.Open(tarantool)
	if err != nil {
		return luaStateUnlisted, fmt.Sprintf("cannot read %s: %v", tarantool, err)
	}

	defer func() { _ = file.Close() }()

	state, ok := symbolState(file.Symbols)
	if ok {
		return state, ""
	}

	state, ok = symbolState(file.DynamicSymbols)
	if ok {
		return state, ""
	}

	unlisted := "neither the .symtab nor the .dynsym of " + tarantool + " lists " + luaStateName

	build := buildID(file)
	if len(build) < 3 { //nolint:mnd // The directory is named by two digits, the file by the rest.
		return luaStateUnlisted, unlisted + ", and it has no build ID to find a debug file by"
	}

	debugFile := debugFilePath(filepath.Join(dir, debugFileDir), build)

	debug, err := elf.Open(debugFile)
	if err != nil {
		return luaStateUnlisted, fmt.Sprintf("%s, and its debug file cannot be read: %v",
			unlisted, err)
	}

	defer func() { _ = debug.Close() }()

	state, ok = symbolState(debug.Symbols)
	if ok {
		return state, ""
	}

	return luaStateUnlisted, unlisted + ", and neither does the .symtab of " + debugFile
}

// symbolState returns what the symbols the function symbols reads tell of
// tarantool_L, and reports false of symbols that cannot be read or do not
// list it.
func symbolState(symbols func() ([]elf.Symbol, error)) (luaState, bool) {
	list, err := symbols()
	if err != nil {
		return luaStateUnlisted, false
	}

	for _, symbol := range list {
		if symbol.Name != luaStateName || symbol.Section == elf.SHN_UNDEF {
			continue
		}

		if elf.ST_TYPE(symbol.Info) == elf.STT_TLS {
			return luaStateThreadLocal, true
		}

		return luaStateGlobal, true
	}

	return luaStateUnlisted, false
}

// readThreadDBFacts gathers the facts of the core of the artefacts in dir,
// whose threads have the stacks stacks, for gdb of the version gdb, zero
// when it cannot be told, on this host (see hostLibraries).
func readThreadDBFacts(dir string, gdb debuggerVersion, stacks threadStacks) threadDBFacts {
	facts := threadDBFacts{
		Release: release{}, LuaState: luaStateUnlisted, LuaStateUnlisted: "", GDB: gdb,
		ZeroedStacks: stacks.zeroed, ThreadDB: false, NoLoad: "", ThreadDBUnknown: "",
		CoreMachine: elf.EM_NONE, HostMachine: elf.EM_NONE, CoreGlibc: glibcVersion{},
		CoreGlibcUnknown: "", HostGlibc: glibcVersion{}, HostGlibcUnknown: "",
	}

	line, err := readVersion(filepath.Join(dir, "version"))
	if err == nil {
		version, ok := parseVersion(line)
		if ok {
			facts.Release, _ = parseRelease(version.Release)
		}
	}

	// gdb 17.1 and later need none of the rest.
	if !gdb.known() || gdb.compare(gdbOwnTLS) >= 0 {
		return facts
	}

	facts.LuaState, facts.LuaStateUnlisted = readLuaState(dir)

	// Nor does a tarantool_L of the process.
	threadLocal, _ := facts.threadLocal()
	if !threadLocal || facts.ZeroedStacks > 0 {
		return facts
	}

	machine, dirs := hostLibraries()

	facts.HostMachine = machine

	libc := findArchiveLibc(dir)
	if libc == "" {
		facts.CoreGlibcUnknown = "the archive has no " + libcName
	} else {
		readArchiveLibc(&facts, libc, machine, dirs)
	}

	hostLibc := findLibrary(dirs, libcName, machine)
	if hostLibc == "" {
		facts.HostGlibcUnknown = "no " + libcName + " under " + strings.Join(dirs, ", ")
	} else {
		facts.HostGlibc, err = libcVersion(hostLibc)
		if err != nil {
			facts.HostGlibcUnknown = err.Error()
		}
	}

	return facts
}

// readArchiveLibc reads into facts those of the libc of the archive at
// libc: its machine and its glibc, and whether a libthread_db is next to it
// and loads in gdb of a host of machine, whose libraries are in dirs.
func readArchiveLibc(facts *threadDBFacts, libc string, machine elf.Machine, dirs []string) {
	threadDB := filepath.Join(filepath.Dir(libc), threadDBName)

	file, err := elf.Open(libc)
	if err == nil {
		facts.CoreMachine = file.Machine

		_ = file.Close()
	}

	facts.ThreadDB = isRegularFile(threadDB)
	if facts.ThreadDB {
		facts.NoLoad, err = threadDBLoadProblem(threadDB, machine, dirs)
		if err != nil {
			facts.ThreadDBUnknown = err.Error()
		}
	}

	facts.CoreGlibc, err = libcVersion(libc)
	if err != nil {
		facts.CoreGlibcUnknown = err.Error()
	}
}

// warnThreadLocalLua warns, before gdb starts, when gdb will not read
// tarantool_L of the core (see threadDBFacts), or may not, as a development
// version of 17 (see gdbOwnTLS): lj-bt, with gdbTools, finds no Lua state
// of the main thread, L=0x0, and shows the Lua frames of the VM entries
// alone, and the lj-* commands given no lua_State find none. The hint says
// why and what to do instead. What cannot be told is said at the debug
// level, and so is a tarantool_L its symbols do not tell of, which the
// version does.
func warnThreadLocalLua(log *slog.Logger, facts threadDBFacts, gdbTools bool) {
	if facts.LuaStateUnlisted != "" {
		log.Debug(fmt.Sprintf("the symbols of the tarantool of the core do not tell whether %s "+
			"is thread-local, the version of the core tells, thread-local since %d.%d: %s",
			luaStateName, threadLocalLua[0], threadLocalLua[1], facts.LuaStateUnlisted))
	}

	problem, instead, unknown := facts.tlsProblem()
	if unknown != "" {
		log.Debug("cannot tell whether gdb reads the Lua state of the main thread of the core, " +
			luaStateName + ": cannot tell " + unknown)
	}

	if problem == "" {
		return
	}

	commands := "the lj-* commands given no lua_State will not find it"
	if gdbTools {
		commands = "lj-bt will show L=0x0 (none) and the Lua frames of the VM entries alone, and " +
			commands
	}

	reads, development := "will not read", ""
	if facts.developmentOwnTLS() {
		reads, commands = "may not read", "if it does not, "+commands
		development = fmt.Sprintf(", unless it is a development version of %d that reads it on "+
			"its own", gdbOwnTLS[0])
	}

	core := "this core"
	if facts.Release != (release{}) {
		core = fmt.Sprintf("this core of Tarantool %d.%d.%d", facts.Release[0], facts.Release[1],
			facts.Release[2])
	}

	warnWithHint(log, fmt.Sprintf("gdb %s %s %s of %s, the Lua state of the main thread, "+
		"thread-local since %d.%d: %s", facts.GDB, reads, luaStateName, core, threadLocalLua[0],
		threadLocalLua[1], commands),
		fmt.Sprintf("gdb before %s needs libthread_db for it%s, and %s; use gdb %s or later, %s",
			gdbOwnTLS, development, problem, gdbOwnTLS, instead))
}
