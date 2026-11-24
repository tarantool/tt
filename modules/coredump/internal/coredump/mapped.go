package coredump

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// artefactsTarantool is the name of the tarantool in the artefacts of an
// archive.
const artefactsTarantool = "tarantool"

// errLinkedElsewhere reports a link linkInRoot made that does not resolve to
// the file it was made to: a directory of its path is a link to another
// directory of the artefacts.
var errLinkedElsewhere = errors.New("the link does not resolve to the file")

// mappedFiles is what linkMappedFiles did of the files the core of the
// artefacts of a directory maps that are not at their paths there, by the
// paths of the NT_FILE note.
type mappedFiles struct {
	// linked are the paths linked to the file of the artefacts that is the
	// file the core maps, which is the value, relative to the directory.
	linked map[string]string
	// failed are the paths that could not be linked, with why, and the file
	// they were to be linked to.
	failed []failedLink
	// data are the paths of the files the artefacts do not hold that hold no
	// code: the core maps none of them executable, as the locale archive and
	// the gconv cache of glibc, in the order of the core.
	data []string
	// code are the paths of the files the artefacts do not hold that the
	// core maps executable: the shared libraries pack did not pack, or not
	// of the build ID the process loaded.
	code []string
}

// failedLink is a path of a file the core maps that linkMappedFiles could
// not link to the file of the artefacts that is that file.
type failedLink struct {
	path, target string
	err          error
}

// linkMappedFiles gives the files the core of the artefacts in dir maps the
// paths its NT_FILE note records of them under dir, where gdb looks them up.
// gdb opens each file of the note under its sysroot, which gdb.sh sets to
// the artefacts, for the pages of its mappings the core does not hold, as
// Linux leaves out the code of the executable and of the shared libraries,
// and warns of each it cannot open: "Can't open file ... during file-backed
// mapping note processing". The archive holds the tarantool as tarantool, and
// the shared libraries by the names the dynamic loader opened them by, while
// the note records the path the tarantool ran from and the real paths of the
// libraries, as /usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2 of
// /lib64/ld-linux-x86-64.so.2 on Debian.
//
// A path dir has nothing at gets a symbolic link, relative, to the file of
// the artefacts that is the file the core maps there (see sameFiles.of):
// gdb then reads those pages from the file the process mapped, where it read
// them from the tarantool and the libraries it loaded instead. gdb 16 and
// later refuse a file of another build ID than the core has of it, an older
// gdb reads it: no such file is linked. A file the artefacts do not hold, as
// the locale archive of glibc, gets no link, and is among the data or the
// code of what linkMappedFiles returns, by how the core maps it. A path
// gdb would not look up under dir is passed over, and so is a link that
// cannot be made, which is among the failed.
func linkMappedFiles(dir string) (mappedFiles, error) {
	files := mappedFiles{linked: map[string]string{}, failed: nil, data: nil, code: nil}

	core, err := openCore(filepath.Join(dir, "coredump"))
	if err != nil {
		return files, err
	}

	defer func() { _ = core.Close() }()

	root, err := os.OpenRoot(dir)
	if err != nil {
		return files, fmt.Errorf("cannot open the artefacts: %w", err)
	}

	defer func() { _ = root.Close() }()

	same := newSameFiles(core, root)

	for _, path := range core.mappedPaths() {
		name, ok := sysrootName(path)
		if !ok {
			continue
		}

		// The file is there, packed, or linked by an inspect before.
		_, err := root.Lstat(name)
		if err == nil {
			continue
		}

		target := same.of(path)

		switch {
		case target != "":
			err = linkInRoot(root, name, target)
			if err != nil {
				files.failed = append(files.failed,
					failedLink{path: path, target: target, err: err})

				continue
			}

			files.linked[path] = target
		case core.mapsCode(path):
			files.code = append(files.code, path)
		default:
			files.data = append(files.data, path)
		}
	}

	return files, nil
}

// sysrootName returns the name relative to the sysroot of gdb that gdb looks
// the path a core records up by: the path without its leading slash. A path
// that is not absolute, is not clean or would leave the sysroot has none.
func sysrootName(path string) (string, bool) {
	name, ok := strings.CutPrefix(path, "/")
	if !ok || !filepath.IsLocal(name) || filepath.Clean(name) != name {
		return "", false
	}

	return name, true
}

// linkInRoot makes name of root a symbolic link to target of root, both
// relative to root, by the path from the directory of name, so that the
// directory may be moved: a link to an absolute path would point out of it.
// The directories of name are made first. A link that does not resolve to
// target, through a link of root in the path of name, is removed.
func linkInRoot(root *os.Root, name, target string) error {
	err := root.MkdirAll(filepath.Dir(name), dirMode)
	if err != nil {
		return fmt.Errorf("cannot create the directory of the link: %w", err)
	}

	relative, err := filepath.Rel(filepath.Dir(name), target)
	if err != nil {
		return fmt.Errorf("cannot point the link to %s: %w", target, err)
	}

	err = root.Symlink(relative, name)
	if err != nil {
		return fmt.Errorf("cannot create the link: %w", err)
	}

	linked, err := root.Stat(name)
	if err == nil {
		var info os.FileInfo

		info, err = root.Stat(target)
		if err == nil && os.SameFile(linked, info) {
			return nil
		}
	}

	_ = root.Remove(name)

	if err != nil {
		return fmt.Errorf("%w: %w", errLinkedElsewhere, err)
	}

	return errLinkedElsewhere
}

// sameFiles finds the file of the artefacts of a directory, root, that is a
// file the core of the artefacts maps.
type sameFiles struct {
	core *coreDump
	// byID are the files gdb loads of the artefacts, the tarantool and the
	// shared libraries, by their build IDs, the first of a build ID: the
	// files the core names, of byName, and those the artefacts have at the
	// paths the core maps.
	byID map[string]string
	// byName are the files of the artefacts the core names for the paths it
	// maps: tarantool for that of the executable, unless the dynamic loader
	// ran it, ld.so TARANTOOL, and the executable is the loader's, and the
	// files of the shared libraries of the link map by the paths of the files
	// the core maps at their dynamic sections.
	byName map[string]string
}

// newSameFiles returns the sameFiles of the core of the artefacts of root.
// Without a tarantool of the artefacts the core names none of its files.
func newSameFiles(core *coreDump, root *os.Root) *sameFiles {
	same := &sameFiles{core: core, byID: map[string]string{}, byName: map[string]string{}}

	candidates := []string{artefactsTarantool}

	binary, err := openInRoot(root, artefactsTarantool)
	if err == nil {
		defer func() { _ = binary.Close() }()

		exe, ok := core.executable()
		if ok && !ranByLoader(core, binary.File) {
			same.byName[exe.path] = artefactsTarantool
		}

		for _, object := range loadedObjects(io.Discard, core, binary.File) {
			name, ok := sysrootName(object.name)
			mapping, mapped := core.mappingAt(object.dynamic)

			if ok && mapped && isRegularIn(root, name) {
				same.byName[mapping.path] = name
				candidates = append(candidates, name)
			}
		}
	}

	for _, path := range core.mappedPaths() {
		name, ok := sysrootName(path)
		if ok && isRegularIn(root, name) {
			candidates = append(candidates, name)
		}
	}

	for _, name := range candidates {
		id := rootBuildID(root, name)
		if _, found := same.byID[id]; id != "" && !found {
			same.byID[id] = name
		}
	}

	return same
}

// of returns the file of the artefacts that is the file the core maps at
// path, relative to the root, or "" when the artefacts have none: the file
// of the build ID of the image of the file in the core, where the core has
// one, else the file the core names for it, of byName. The core holds the
// first page of every ELF file mapped, with the build ID in it, unless the
// coredump_filter of the process left it out (see core(5)); a file the core
// has another build ID of is not the one.
func (s *sameFiles) of(path string) string {
	id := s.core.mappedBuildID(path)
	if id != "" {
		return s.byID[id]
	}

	return s.byName[path]
}

// ranByLoader reports whether the dynamic loader ran binary, ld.so BINARY,
// as the process of core: the kernel loaded no interpreter, AT_BASE, for a
// binary that has one, and the executable of AT_PHDR is the loader.
func ranByLoader(core *coreDump, binary *elf.File) bool {
	base, ok := core.auxv[atBase]

	return ok && base == 0 && interpreter(binary) != ""
}

// rootELF is an ELF file of a root, open for reading.
type rootELF struct {
	*elf.File

	file *os.File
}

// Close closes the file.
func (f rootELF) Close() error {
	err := f.file.Close()
	if err != nil {
		return fmt.Errorf("cannot close %s: %w", f.file.Name(), err)
	}

	return nil
}

// openInRoot opens the ELF file name of root.
func openInRoot(root *os.Root, name string) (rootELF, error) {
	file, err := root.Open(name)
	if err != nil {
		return rootELF{File: nil, file: nil}, fmt.Errorf("cannot open %s: %w", name, err)
	}

	binary, err := elf.NewFile(file)
	if err != nil {
		_ = file.Close()

		return rootELF{File: nil, file: nil}, fmt.Errorf("cannot read %s: %w", name, err)
	}

	return rootELF{File: binary, file: file}, nil
}

// isRegularIn reports whether name of root is a regular file, or a link to
// one in root.
func isRegularIn(root *os.Root, name string) bool {
	info, err := root.Stat(name)

	return err == nil && info.Mode().IsRegular()
}

// rootBuildID returns the GNU build ID of the ELF executable or shared
// object name of root, or "" when it is none or has none.
func rootBuildID(root *os.Root, name string) string {
	file, err := openInRoot(root, name)
	if err != nil {
		return ""
	}

	defer func() { _ = file.Close() }()

	if file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		return ""
	}

	return buildID(file.File)
}

// mappedPaths returns the paths of the files the core maps, each once, in
// the order of their first mappings.
func (c *coreDump) mappedPaths() []string {
	var paths []string

	seen := map[string]bool{}

	for _, mapping := range c.mappings {
		if !seen[mapping.path] {
			seen[mapping.path] = true
			paths = append(paths, mapping.path)
		}
	}

	return paths
}

// mappedBuildID returns the GNU build ID of the image the core holds of the
// file it maps at path, of a mapping of the file from its first byte, or ""
// when the core holds none, or none with a build ID.
func (c *coreDump) mappedBuildID(path string) string {
	for _, mapping := range c.mappings {
		if mapping.path != path || mapping.offset != 0 {
			continue
		}

		image, err := c.imageAt(mapping.start)
		if err != nil {
			continue
		}

		id := c.imageBuildID(image)
		if id != "" {
			return id
		}
	}

	return ""
}

// mapsCode reports whether the core maps the file at path executable: a
// segment of the memory of a mapping of it is, whether the core holds its
// content or not. Linux and gcore of gdb write a program header of every
// mapping, with its permissions.
func (c *coreDump) mapsCode(path string) bool {
	for _, mapping := range c.mappings {
		if mapping.path != path {
			continue
		}

		if slices.ContainsFunc(c.file.Progs, func(prog *elf.Prog) bool {
			return prog.Type == elf.PT_LOAD && prog.Flags&elf.PF_X != 0 &&
				prog.Vaddr < mapping.end && mapping.start < prog.Vaddr+prog.Memsz
		}) {
			return true
		}
	}

	return false
}

// shownMapped is how many paths of files the core maps a line of
// logMappedFiles names.
const shownMapped = 2

// gdbMappedFiles is the first release of gdb that reads the files a core
// maps from its NT_FILE note, and warns of each it cannot open ("Can't open
// file ... during file-backed mapping note processing"): gdb 9.2 has no such
// processing, and warns of none.
var gdbMappedFiles = debuggerVersion{10, 1, 0}

// logMappedFiles logs to log what linkMappedFiles did, files, before gdb, of
// version gdb, starts: gdb warns of each file the core maps that is not at
// its path in the artefacts. Of the files that hold no code, which the
// archive does not hold, the line says so, since the warnings look like an
// archive gone wrong. The paths linkMappedFiles could not link, the files of
// other paths of the archive, are a line too: gdb warns of them as well. The
// links made and the files of code that are not in the archive, of which gdb
// has no symbols, as it says, are debug lines. A gdb before gdbMappedFiles
// warns of no file the core maps: what it would be told of them are debug
// lines too, and say nothing of warnings. A gdb of a version that cannot be
// told is taken for one that warns, as any of the last years does.
func logMappedFiles(log *slog.Logger, files mappedFiles, gdb debuggerVersion) {
	for _, path := range slices.Sorted(maps.Keys(files.linked)) {
		log.Debug(fmt.Sprintf("the core maps %s, linked to %s of the artefacts", path,
			files.linked[path]))
	}

	if len(files.code) > 0 {
		log.Debug(fmt.Sprintf("the core maps %d files of code that are not in the archive, "+
			"which gdb has no symbols of: %s", len(files.code),
			namePaths(files.code, len(files.code))))
	}

	if gdb.known() && gdb.compare(gdbMappedFiles) < 0 {
		for _, failed := range files.failed {
			log.Debug(fmt.Sprintf("cannot link %s, a file the core maps, to %s of the archive: "+
				"%v", failed.path, failed.target, failed.err))
		}

		if len(files.data) > 0 {
			log.Debug(fmt.Sprintf("the core maps %d files that are not in the archive and hold "+
				"no code gdb needs: %s", len(files.data),
				namePaths(files.data, len(files.data))))
		}

		return
	}

	switch first := files.failed; len(first) {
	case 0:
	case 1:
		log.Info(fmt.Sprintf("cannot link %s, a file the core maps, to %s of the archive: %v: "+
			"gdb will warn that it can't open it", first[0].path, first[0].target, first[0].err))
	default:
		log.Info(fmt.Sprintf("cannot link %d files the core maps to those of the archive, %s to "+
			"%s: %v: gdb will warn that it can't open them", len(first), first[0].path,
			first[0].target, first[0].err))
	}

	switch len(files.data) {
	case 0:
	case 1:
		log.Info(fmt.Sprintf("gdb will warn of a file the core maps that is not in the "+
			"archive, %s: it holds no code gdb needs", files.data[0]))
	default:
		log.Info(fmt.Sprintf("gdb will warn of %d files the core maps that are not in the "+
			"archive, %s: they hold no code gdb needs", len(files.data),
			namePaths(files.data, shownMapped)))
	}
}

// namePaths returns the first shown of paths, joined by commas and an
// "and", and how many more there are: "A, B and 3 more".
func namePaths(paths []string, shown int) string {
	if len(paths) <= shown {
		if len(paths) == 1 {
			return paths[0]
		}

		return strings.Join(paths[:len(paths)-1], ", ") + " and " + paths[len(paths)-1]
	}

	return fmt.Sprintf("%s and %d more", strings.Join(paths[:shown], ", "), len(paths)-shown)
}
