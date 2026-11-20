package cc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tarantool/tt/sdk/build/v3"
)

// This file is the cache of a build: an object, and the module, are made
// again only when what they are made of changed. It is the depend mode of
// ccache, and what ninja and Zig do: the key of an object is a hash of the
// command line, the compiler and the environment it reads, and the record
// of the object lists the files its last compile read, from its depfile,
// with the hashes of their content. An object is up to date when its
// record has its key and each of the files still has its hash, the source
// and every header, those of the system included; a file gone is a change.
// The cache is in the work directory, which the core empties for a build
// from scratch (build.Context.Fresh), and is bounded by the sources: one
// object a source, overwritten, and those of sources no longer listed
// removed.

// cacheSchema is the version of the keys and the records: another one makes
// every key another, so a tt whose cache differs rebuilds everything: 2
// keeps the output of the compiler in the record of an object.
const cacheSchema = "tt-cc-2"

// compilerEnv are the variables of the environment of a compile the compilers
// read themselves whatever the language, and so are of the key of every
// object: those cc-rs lists for gcc, clang, zig cc and the tools of Xcode
// (rerun-if-env-changed), with the two gcc writes a list of dependencies
// by, and the deployment target of iOS. The directories of headers of one
// language are of the objects of that language alone (languageEnv); those
// of Objective-C, OBJC_INCLUDE_PATH and OBJCPLUS_INCLUDE_PATH, of none, as
// tt compiles no Objective-C. TMPDIR is left out, as cc-rs leaves it out:
// it says where the temporary files of a compile go, not what the object
// is; LIBRARY_PATH is of the link alone (linkEnv), and PATH is not either:
// the compiler is known by its path and its version (compilerIdentity).
var compilerEnv = []string{
	"CPATH", "COMPILER_PATH", "SOURCE_DATE_EPOCH", "GCC_EXEC_PREFIX",
	"DEPENDENCIES_OUTPUT", "SUNPRO_DEPENDENCIES",
	"CCC_OVERRIDE_OPTIONS", "CLANG_NO_DEFAULT_CONFIG",
	"ZIG_LIB_DIR", "ZIG_LIBC",
	"DEVELOPER_DIR", "TOOLCHAINS", "SDKROOT", "MACOSX_DEPLOYMENT_TARGET",
	"IPHONEOS_DEPLOYMENT_TARGET", "RC_DEBUG_OPTIONS", "RC_DEBUG_PREFIX_MAP",
}

// languageEnv returns the variables of the environment a compile of lang
// reads: compilerEnv and the directories of headers of the language,
// C_INCLUDE_PATH of C and of assembler, which gcc preprocesses as C, and
// CPLUS_INCLUDE_PATH of C++.
func languageEnv(lang language) []string {
	if lang == langCXX {
		return concat(compilerEnv, []string{"CPLUS_INCLUDE_PATH"})
	}

	return concat(compilerEnv, []string{"C_INCLUDE_PATH"})
}

// linkEnv are the variables of the environment of the link the compiler
// that links reads: those of compilerEnv, and LIBRARY_PATH, where gcc looks
// for the libraries of -l.
var linkEnv = append(append([]string(nil), compilerEnv...), "LIBRARY_PATH")

// envOf returns the variables of names that environ sets, as key=value, in
// the order of names: what of the environment is of a key.
func envOf(environ []string, names []string) []string {
	var set []string

	for _, name := range names {
		value, ok := build.LookupEnv(environ, name)
		if ok {
			set = append(set, name+"="+value)
		}
	}

	return set
}

// keyHasher writes the parts of a key, each with its length, so that no two
// lists of parts make one key.
type keyHasher struct {
	hash hash.Hash
}

// newKey returns a keyHasher of the schema and the kind of key.
func newKey(kind string) keyHasher {
	key := keyHasher{hash: sha256.New()}

	key.add(cacheSchema, kind)

	return key
}

// add writes parts into the key, after a mark of how many they are.
func (k keyHasher) add(parts ...string) {
	_, _ = fmt.Fprintf(k.hash, "#%d;", len(parts))

	for _, part := range parts {
		_, _ = fmt.Fprintf(k.hash, "%d:%s;", len(part), part)
	}
}

// sum returns the key, in hex.
func (k keyHasher) sum() string {
	return hex.EncodeToString(k.hash.Sum(nil))
}

// objectKey is the key of the object of step compiled in cwd for target,
// with the compiler whose identity is identity in an environment of which
// env are the variables the compiler reads (languageEnv): what but the
// files it reads makes the object. The command line is that of the compile
// but its paths of the depfile and of the object, which name where it
// writes, not what (compileStep.args).
func objectKey(step compileStep, cwd string, target build.Target, identity string,
	env []string,
) string {
	key := newKey("object")

	key.add(step.lang.String())
	key.add(target.OS, target.Arch)
	key.add(cwd)
	key.add(identity)
	key.add(env...)
	key.add(step.compiler...)
	key.add(step.flags...)
	key.add(step.source)

	return key.sum()
}

// objectRecord is the record of an object, in <object>.json: what it was
// compiled of.
type objectRecord struct {
	// Key is the objectKey of the compile.
	Key string `json:"key"`
	// Object is the hash of the content of the object.
	Object string `json:"object"`
	// Inputs are the files the compile read, the source first, as its
	// depfile listed them, by absolute path, with the hashes of their
	// content.
	Inputs []input `json:"inputs"`
	// Output is what the compiler printed, its warnings: shown again when
	// the object is taken from the cache, as cargo shows the warnings of a
	// crate it does not compile again, so that a warning does not vanish
	// with the second build.
	Output string `json:"output,omitempty"`
}

// input is a file a compile or a link read, and the hash of its content.
type input struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// recordPath is the path of the record of the object of step.
func (s compileStep) recordPath() string {
	return s.object + ".json"
}

// fileHashes hashes the content of files once a build: the compiles of a
// build read the headers of the system all, and run at once.
type fileHashes struct {
	mu     sync.Mutex
	hashes map[string]string
}

// newFileHashes returns an empty fileHashes.
func newFileHashes() *fileHashes {
	return &fileHashes{mu: sync.Mutex{}, hashes: map[string]string{}}
}

// of returns the hash of the content of the file path, hashed once.
func (f *fileHashes) of(path string) (string, error) {
	sum, ok := f.lookup(path)
	if ok {
		return sum, nil
	}

	sum, err := hashFile(path)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.hashes[path] = sum

	return sum, nil
}

// lookup returns the hash of path, if it was hashed.
func (f *fileHashes) lookup(path string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sum, ok := f.hashes[path]

	return sum, ok
}

// hashFile returns the sha256 of the content of the file path, in hex.
func hashFile(path string) (string, error) {
	// A file a compile read, or an object or a module the build made.
	file, err := os.Open(path) //nolint:gosec // See above.
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	sum := sha256.New()

	_, err = io.Copy(sum, file)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}

	return hex.EncodeToString(sum.Sum(nil)), nil
}

// upToDate reports whether the record of the object of step, if it has one,
// has the key key, the object it records is there, and each of the files of
// the record still has its hash, and returns the record when it does. Any
// file that cannot be read, a record gone or broken included, makes the
// object stale: the cache never fails a build, it compiles.
func upToDate(step compileStep, key string, hashes *fileHashes) (*objectRecord, bool) {
	data, err := os.ReadFile(step.recordPath())
	if err != nil {
		return nil, false
	}

	var record objectRecord

	err = json.Unmarshal(data, &record)
	if err != nil || record.Key != key || len(record.Inputs) == 0 {
		return nil, false
	}

	object, err := hashFile(step.object)
	if err != nil || object != record.Object {
		return nil, false
	}

	if !inputsUnchanged(record.Inputs, hashes) {
		return nil, false
	}

	return &record, true
}

// inputsUnchanged reports whether each of inputs still has its hash.
func inputsUnchanged(inputs []input, hashes *fileHashes) bool {
	for _, in := range inputs {
		sum, err := hashes.of(in.Path)
		if err != nil || sum != in.SHA256 {
			return false
		}
	}

	return true
}

// recordObject writes the record of the object of step, just compiled in
// cwd with the key key: the files of its depfile - the source alone for
// assembler as it is, which the preprocessor does not read - with their
// hashes, the hash of the object, and printed, what the compiler printed.
// It returns an error when the depfile is not there or not one, and the
// object is then compiled again next time: without the list of the headers
// it read, nothing would tell it stale.
func recordObject(step compileStep, cwd, key, printed string, hashes *fileHashes) error {
	var files []string

	if step.lang == langAsm {
		files = []string{step.source}
	} else {
		data, err := os.ReadFile(step.depfile())
		if err != nil {
			return fmt.Errorf("reading the depfile: %w", err)
		}

		files, err = parseDepfile(string(data))
		if err != nil {
			return fmt.Errorf("parsing the depfile %s: %w", step.depfile(), err)
		}

		if len(files) == 0 {
			return fmt.Errorf("%w: %s", errEmptyDepfile, step.depfile())
		}
	}

	record := objectRecord{
		Key: key, Object: "", Inputs: make([]input, 0, len(files)), Output: printed,
	}

	for _, file := range files {
		if !filepath.IsAbs(file) {
			file = filepath.Join(cwd, file)
		}

		sum, err := hashes.of(file)
		if err != nil {
			return err
		}

		record.Inputs = append(record.Inputs, input{Path: file, SHA256: sum})
	}

	sum, err := hashFile(step.object)
	if err != nil {
		return err
	}

	record.Object = sum

	return writeRecord(step.recordPath(), record)
}

// errEmptyDepfile reports a depfile that lists no file, not even the source.
var errEmptyDepfile = errors.New("the depfile lists no file")

// writeRecord writes record as the JSON of the file path, atomically.
func writeRecord(path string, record any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return writeFileAtomic(path, append(data, '\n'))
}

// removeRecord removes the record of the file path, as a compile or a link
// does before it starts: one stopped halfway leaves no record, and what it
// left is made again.
func removeRecord(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", path, err)
	}

	return nil
}

// compilerIdentity is what tells a compiler from another in a key: the path
// of its program as the PATH of the build resolves it, the size and the time
// of the change of that file, the machine it compiles for (-dumpmachine) and
// the first line of its --version, each run with the words of the compiler,
// so that a wrapper, ccache cc, says the version of what it wraps. Each part
// that cannot be told says why, so that it is still a part of the key: a
// compiler that is not there fails its compile, which says so. It runs the
// commands itself, as compile does, so that a probe that fails prints
// nothing.
func compilerIdentity(ctx context.Context, c *build.Context, words []string) string {
	parts := []string{"compiler " + commandLine(words)}

	path, err := c.LookPath(words[0])
	if err == nil {
		info, statErr := os.Stat(path)
		if statErr == nil {
			parts = append(parts, fmt.Sprintf("path %s size %d mtime %s", path, info.Size(),
				info.ModTime().UTC().Format(time.RFC3339Nano)))
		} else {
			parts = append(parts, "path "+path+": "+statErr.Error())
		}
	} else {
		parts = append(parts, "path: "+err.Error())
	}

	for _, flag := range []string{"-dumpmachine", "--version"} {
		argv := concat(words, []string{flag})
		cmd := c.Command(ctx, argv[0], argv[1:]...)

		var stdout, stderr bytes.Buffer

		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		logger(c).DebugContext(ctx, "probing the compiler", "command", commandLine(cmd.Args))

		err := cmd.Run()
		if err != nil {
			parts = append(parts, flag+": "+err.Error())

			continue
		}

		first, _, _ := strings.Cut(stdout.String(), "\n")

		parts = append(parts, flag+" "+strings.TrimSpace(first))
	}

	return strings.Join(parts, "\n")
}

// linkRecordFile is the record of the link, in the work directory.
const linkRecordFile = "link.json"

// linkRecord is the record of the link of a module.
type linkRecord struct {
	// Key is the linkKey of the link.
	Key string `json:"key"`
	// Module is the hash of the content of the module it made.
	Module string `json:"module"`
}

// linkKey is the key of the link of built: its command line, the compiler
// that links and the environment it reads (linkEnv), the target, the
// content of each object in their order, and that of the libraries of
// libraries, each a file it may link (libraryArgs.files): the files of
// libraries, the static libraries a build with static = true takes, and
// what each library may be in library_dirs (libraryFiles). Another library
// found elsewhere - of the system, or by a flag of ldflags - and a file a
// flag names, a version script say, are not of the key: such a change is
// built by tt package build --rebuild.
func linkKey(built plan, target build.Target, identity string, env []string,
	libraries []string, hashes *fileHashes,
) (string, error) {
	key := newKey("link")

	key.add(target.OS, target.Arch)
	key.add(identity)
	key.add(env...)
	key.add(built.link...)

	for _, step := range built.steps {
		sum, err := hashFile(step.object)
		if err != nil {
			return "", err
		}

		key.add(step.object, sum)
	}

	// A library that is not there is of the key as well: one that appears
	// may be the one the linker takes, a .so beside the .a it took.
	for _, library := range libraries {
		sum, err := hashes.of(library)
		if err != nil {
			sum = "none"
		}

		key.add(library, sum)
	}

	return key.sum(), nil
}

// libraryFiles are the files the libraries of config, of the build for goos,
// may be in library_dirs, relative to cwd: lib<name>.a, .so, .dylib and
// .tbd of each library in each directory, and the file of a library of
// GNU ld's :<file>. Those that are not there are left to the key to skip.
func libraryFiles(config Config, goos, cwd string) []string {
	overlay := config.Platforms[goos]

	var files []string

	for _, dir := range concat(config.LibraryDirs, overlay.LibraryDirs) {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}

		for _, library := range concat(config.Libraries, overlay.Libraries) {
			if isLibraryFile(library) {
				// A file is of the key by its path (planLibraries).
				continue
			}

			if file, ok := strings.CutPrefix(library, ":"); ok {
				files = append(files, filepath.Join(dir, file))

				continue
			}

			for _, ext := range []string{".a", ".so", ".dylib", ".tbd"} {
				files = append(files, filepath.Join(dir, "lib"+library+ext))
			}
		}
	}

	return files
}

// linkUpToDate reports whether the record of the link in work has the given
// key and the module out is still the one it made.
func linkUpToDate(work, out, key string) bool {
	data, err := os.ReadFile(filepath.Join(work, linkRecordFile))
	if err != nil {
		return false
	}

	var record linkRecord

	err = json.Unmarshal(data, &record)
	if err != nil || record.Key != key {
		return false
	}

	sum, err := hashFile(out)

	return err == nil && sum == record.Module
}

// recordLink writes the record of the link of the module out, of the given
// key, into work.
func recordLink(work, out, key string) error {
	sum, err := hashFile(out)
	if err != nil {
		return err
	}

	return writeRecord(filepath.Join(work, linkRecordFile), linkRecord{Key: key, Module: sum})
}

// dropCache removes the cache of the build of work: the objects and their
// records, the record of the link and that of the flags probed. The core
// empties the work directory of a build from scratch itself; a build told
// Fresh drops what it keeps all the same, so that nothing of an earlier
// build is reused whoever runs it.
func dropCache(work string) error {
	for _, path := range []string{
		objectDir(work), filepath.Join(work, linkRecordFile), filepath.Join(work, probeFile),
	} {
		err := os.RemoveAll(path)
		if err != nil {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}

	return nil
}
