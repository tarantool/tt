package cc

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// TestParseDepfile pins the reading of depfiles as gcc and clang write them.
func TestParseDepfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"one rule", "a.o: a.c a.h\n", []string{"a.c", "a.h"}},
		{"no newline at the end", "a.o: a.c", []string{"a.c"}},
		{"continuations", "a.o: a.c \\\n  /usr/include/stdio.h \\\n  b.h\n",
			[]string{"a.c", "/usr/include/stdio.h", "b.h"}},
		{"CRLF", "a.o: a.c \\\r\n  b.h\r\n", []string{"a.c", "b.h"}},
		{"an escaped space", `a.o: my\ dir/a.c b.h`, []string{"my dir/a.c", "b.h"}},
		{"$$ and \\#", `a.o: $$x/a.c \#x.h`, []string{"$x/a.c", "#x.h"}},
		{"a backslash of a path", `a.o: a\b.h`, []string{`a\b.h`}},
		{"a colon of a path", "a.o: a:b.h c.h", []string{"a:b.h", "c.h"}},
		{"several targets", "a.o a.d: a.c\n", []string{"a.c"}},
		{"a quoted target", `/w/my\ obj/a.o: a.c`, []string{"a.c"}},
		{"phony rules of -MP", "a.o: a.c a.h b.h\n\na.h:\n\nb.h:\n", []string{"a.c", "a.h", "b.h"}},
		{"several rules", "a.o: a.c\nb.o: b.c a.c\n", []string{"a.c", "b.c"}},
		{"duplicates", "a.o: a.c a.h a.c a.h\n", []string{"a.c", "a.h"}},
		{"a target with no space before the colon", "a.o:a.c\n", nil},
		{"tabs", "a.o:\ta.c\tb.h\n", []string{"a.c", "b.h"}},
		{"blank lines", "\n\n  \na.o: a.c\n\n", []string{"a.c"}},
		{"a backslash at the end", `a.o: a.c \`, []string{"a.c", `\`}},
	}

	for _, test := range tests {
		got, err := parseDepfile(test.in)
		if test.want == nil {
			require.Error(t, err, test.name)

			continue
		}

		require.NoError(t, err, test.name)
		assert.Equal(t, test.want, got, test.name)
	}

	for name, in := range map[string]string{
		"no colon":     "a.o a.c\n",
		"no target":    ": a.c\n",
		"a lone colon": "a.o: a.c\n: b.c\n",
	} {
		_, err := parseDepfile(in)
		require.Error(t, err, name)
	}
}

// TestParseDepfileOfGcc reads a depfile of the shape gcc 15 writes with -MD
// -MQ for a source and an object of paths with spaces and dollars.
func TestParseDepfileOfGcc(t *testing.T) {
	t.Parallel()

	got, err := parseDepfile("/home/me/my\\ proj/_build/component/n/obj/a.c.1.o: " +
		"my\\ src/a.c \\\n /usr/include/stdc-predef.h my\\ src/$$HOME.h \\\n" +
		" /usr/include/stdio.h\n")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"my src/a.c", "/usr/include/stdc-predef.h", "my src/$HOME.h", "/usr/include/stdio.h",
	}, got)
}

// keyStep is the compile step of the key tests.
func keyStep() compileStep {
	return compileStep{
		source: "a.c", lang: langC, object: "/w/obj/a.c.1.o",
		compiler: []string{"cc"}, flags: []string{"-O2", "-fPIC"},
	}
}

// TestObjectKey checks that the key of an object changes with each of what
// it is made of but the files it reads, and only with that.
func TestObjectKey(t *testing.T) {
	t.Parallel()

	target := build.Target{OS: "linux", Arch: "amd64"}
	env := []string{"CPATH=/x"}
	base := objectKey(keyStep(), "/src", target, "gcc 15", env)

	assert.Equal(t, base, objectKey(keyStep(), "/src", target, "gcc 15", env))

	moved := keyStep()

	moved.object = "/elsewhere/a.o"

	assert.Equal(t, base, objectKey(moved, "/src", target, "gcc 15", env),
		"where the object is written is not of the key")

	arm := build.Target{OS: "linux", Arch: "arm64"}
	darwin := build.Target{OS: "darwin", Arch: "amd64"}
	changes := map[string]string{
		"cwd":      objectKey(keyStep(), "/other", target, "gcc 15", env),
		"arch":     objectKey(keyStep(), "/src", arm, "gcc 15", env),
		"os":       objectKey(keyStep(), "/src", darwin, "gcc 15", env),
		"compiler": objectKey(keyStep(), "/src", target, "gcc 14", env),
		"env":      objectKey(keyStep(), "/src", target, "gcc 15", []string{"CPATH=/y"}),
		"no env":   objectKey(keyStep(), "/src", target, "gcc 15", nil),
	}

	for _, change := range []func(*compileStep){
		func(s *compileStep) { s.source = "b.c" },
		func(s *compileStep) { s.lang = langAsmCpp },
		func(s *compileStep) { s.compiler = []string{"ccache", "cc"} },
		func(s *compileStep) { s.flags = []string{"-O2"} },
		func(s *compileStep) { s.flags = []string{"-fPIC", "-O2"} },
		// One word or two: the parts are delimited.
		func(s *compileStep) { s.compiler, s.flags = []string{"cc -O2"}, []string{"-fPIC"} },
	} {
		step := keyStep()

		change(&step)

		changes[commandLine(step.args("x"))+" "+step.lang.String()] = objectKey(step, "/src",
			target, "gcc 15", env)
	}

	keys := make([]string, 0, 1+len(changes))

	keys = append(keys, base)

	for name, key := range changes {
		assert.NotEqual(t, base, key, name)

		keys = append(keys, key)
	}

	assert.Len(t, slices.Compact(slices.Sorted(slices.Values(keys))), len(keys))
}

// TestEnvOf pins what of the environment is of a key: the variables of the
// list that are set, an empty one included, the last of a duplicate.
func TestEnvOf(t *testing.T) {
	t.Parallel()

	environ := []string{"PATH=/bin", "CPATH=/a", "TMPDIR=/tmp", "SDKROOT=", "CPATH=/b",
		"LIBRARY_PATH=/l"}

	assert.Equal(t, []string{"CPATH=/b", "SDKROOT="}, envOf(environ, compilerEnv))
	assert.Equal(t, []string{"CPATH=/b", "SDKROOT=", "LIBRARY_PATH=/l"}, envOf(environ, linkEnv))
	assert.Empty(t, envOf([]string{"PATH=/bin", "TMPDIR=/tmp"}, linkEnv))

	// The include paths of one language are of its objects alone, those of
	// Objective-C of none.
	paths := []string{"C_INCLUDE_PATH=/c", "CPLUS_INCLUDE_PATH=/cxx", "OBJC_INCLUDE_PATH=/m",
		"OBJCPLUS_INCLUDE_PATH=/mm", "CPATH=/all"}

	assert.Equal(t, []string{"CPATH=/all", "C_INCLUDE_PATH=/c"},
		envOf(paths, languageEnv(langC)))
	assert.Equal(t, []string{"CPATH=/all", "C_INCLUDE_PATH=/c"},
		envOf(paths, languageEnv(langAsmCpp)))
	assert.Equal(t, []string{"CPATH=/all", "CPLUS_INCLUDE_PATH=/cxx"},
		envOf(paths, languageEnv(langCXX)))
	assert.Equal(t, []string{"CPATH=/all"}, envOf(paths, linkEnv))
}

// TestLinkKey checks that the key of the link changes with its command line,
// the compiler, the environment, the target, the content of each object and
// that of a library, one that appears too.
func TestLinkKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	objA := filepath.Join(dir, "a.o")
	objB := filepath.Join(dir, "b.o")
	lib := filepath.Join(dir, "libz.a")
	shared := filepath.Join(dir, "libz.so")

	write := func(path, body string) {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}

	write(objA, "a")
	write(objB, "b")
	write(lib, "z")

	built := plan{
		steps:  []compileStep{{object: objA}, {object: objB}},
		linker: []string{"cc"},
		link:   []string{"cc", "-shared", "-o", "/out/m.so", objA, objB},
	}
	target := build.Target{OS: "linux", Arch: "amd64"}
	libraries := []string{lib, shared}

	key := func(built plan, target build.Target, identity string, env []string) string {
		sum, err := linkKey(built, target, identity, env, libraries, newFileHashes())
		require.NoError(t, err)

		return sum
	}

	base := key(built, target, "gcc", nil)
	assert.Equal(t, base, key(built, target, "gcc", nil))

	other := built

	other.link = []string{"cc", "-shared", "-s", "-o", "/out/m.so", objA, objB}

	assert.NotEqual(t, base, key(other, target, "gcc", nil), "argv")
	assert.NotEqual(t, base, key(built, target, "clang", nil), "compiler")
	assert.NotEqual(t, base, key(built, target, "gcc", []string{"LIBRARY_PATH=/l"}), "env")
	assert.NotEqual(t, base, key(built, build.Target{OS: "linux", Arch: "arm64"}, "gcc", nil),
		"target")

	write(objB, "b2")
	assert.NotEqual(t, base, key(built, target, "gcc", nil), "an object")
	write(objB, "b")
	assert.Equal(t, base, key(built, target, "gcc", nil))

	write(lib, "z2")
	assert.NotEqual(t, base, key(built, target, "gcc", nil), "a library")
	write(lib, "z")

	write(shared, "so")
	assert.NotEqual(t, base, key(built, target, "gcc", nil), "a library that appears")
	require.NoError(t, os.Remove(shared))

	require.NoError(t, os.Remove(objA))

	_, err := linkKey(built, target, "gcc", nil, libraries, newFileHashes())
	require.Error(t, err, "an object that is not there")
}

// TestLibraryFiles pins the files of the libraries of the key of the link.
func TestLibraryFiles(t *testing.T) {
	t.Parallel()

	config := moduleConfig("a.c")

	config.LibraryDirs = []string{"vendor", "/opt/lib"}
	config.Libraries = []string{"z", ":libfoo.a"}
	config.Platforms = map[string]Overlay{"linux": {Libraries: []string{"rt"}}}

	assert.Equal(t, []string{
		"/src/vendor/libz.a", "/src/vendor/libz.so", "/src/vendor/libz.dylib",
		"/src/vendor/libz.tbd", "/src/vendor/libfoo.a",
		"/src/vendor/librt.a", "/src/vendor/librt.so", "/src/vendor/librt.dylib",
		"/src/vendor/librt.tbd",
		"/opt/lib/libz.a", "/opt/lib/libz.so", "/opt/lib/libz.dylib", "/opt/lib/libz.tbd",
		"/opt/lib/libfoo.a",
		"/opt/lib/librt.a", "/opt/lib/librt.so", "/opt/lib/librt.dylib", "/opt/lib/librt.tbd",
	}, libraryFiles(config, "linux", "/src"))
	assert.Empty(t, libraryFiles(moduleConfig("a.c"), "linux", "/src"))
}

// fakeCompilers is the environment of the fake compilers.
func fakeCompilers() map[string]string {
	return map[string]string{"CC": "fakecc", "CXX": "fakec++"}
}

// cachedHarness is a harness of the c build of sources, each written into
// the working directory, with the fake compilers.
func cachedHarness(t *testing.T, sources ...string) (*buildtest.Harness, ccBuild) {
	t.Helper()

	harness, bld := fakeHarness(t, C(),
		map[string]any{"module": "m", "sources": toAny(sources)}, fakeCompilers())

	for _, source := range sources {
		writeFile(t, harness, source, "/* "+source+" */\n")
	}

	return harness, bld
}

// toAny is strings as a TOML array decodes.
func toAny(strings []string) []any {
	out := make([]any, 0, len(strings))

	for _, s := range strings {
		out = append(out, s)
	}

	return out
}

// writeFile writes body into the file name of the working directory of
// harness, which may be in a directory of it.
func writeFile(t *testing.T, harness *buildtest.Harness, name, body string) {
	t.Helper()

	path := filepath.Join(harness.Context.Dirs.Cwd, name)

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

// compiled are the sources the compiles of the calls compiled, sorted, and
// whether the calls link.
func compiled(ran []string) ([]string, bool) {
	var (
		sources []string
		linked  bool
	)

	for _, call := range ran {
		_, after, ok := strings.Cut(call, " -c ")
		if !ok {
			linked = true

			continue
		}

		source, _, _ := strings.Cut(after, " ")

		sources = append(sources, source)
	}

	slices.Sort(sources)

	return sources, linked
}

// rebuild runs bld in harness again and returns what it compiled, and
// whether it linked, since the build before.
func rebuild(t *testing.T, harness *buildtest.Harness, bld ccBuild) ([]string, bool) {
	t.Helper()

	before := len(calls(t, harness))

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())

	return compiled(calls(t, harness)[before:])
}

// TestCacheJobs checks that the jobs of a build are of no key: a build with
// other jobs, and so another TT_BUILD_JOBS in the environment of the
// compiles, compiles and links nothing that is up to date.
func TestCacheJobs(t *testing.T) {
	t.Parallel()

	harness, bld := cachedHarness(t, "a.c", "b.c", "sub/c.cpp")

	sources, linked := rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c", "b.c", "sub/c.cpp"}, sources)
	assert.True(t, linked)

	for _, jobs := range []int{1, 7, 0} {
		harness.SetJobs(jobs)

		sources, linked = rebuild(t, harness, bld)
		assert.Empty(t, sources, "jobs %d", jobs)
		assert.False(t, linked, "jobs %d", jobs)
	}

	// A change is compiled whatever the jobs.
	harness.SetJobs(2)
	writeFile(t, harness, "b.c", "/* b.c changed */\n")

	sources, linked = rebuild(t, harness, bld)
	assert.Equal(t, []string{"b.c"}, sources)
	assert.True(t, linked)
}

// TestMaxJobs checks the size of the pool of the compiles: the Jobs of the
// build, or one a CPU when it is 0.
func TestMaxJobs(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, C(), component())

	assert.Equal(t, runtime.NumCPU(), maxJobs(harness.Context))

	for _, jobs := range []int{1, 3, 128} {
		harness.SetJobs(jobs)

		assert.Equal(t, jobs, maxJobs(harness.Context))
	}

	harness.SetJobs(0)

	assert.Equal(t, runtime.NumCPU(), maxJobs(harness.Context))
}

// TestCache checks that a build compiles only the objects whose inputs
// changed, and links only when an object or the link changed or the module
// is gone.
func TestCache(t *testing.T) {
	t.Parallel()

	harness, bld := cachedHarness(t, "a.c", "b.c", "sub/c.cpp")
	work := harness.Context.Dirs.Work
	module := filepath.Join(harness.Context.Dirs.Modules, "m"+harness.Context.Target.ModuleExt())

	sources, linked := rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c", "b.c", "sub/c.cpp"}, sources)
	assert.True(t, linked)

	sources, linked = rebuild(t, harness, bld)
	assert.Empty(t, sources, "nothing changed")
	assert.False(t, linked, "nothing changed")
	// Three compiles and a link, the two probes of each of the two
	// compilers in each build, and the probes of the two reproducible flags
	// of each in the first build alone, which keeps the answers.
	assert.Len(t, allCalls(t, harness), 3+1+2*2+2*2+2*2, "the compilers are probed each build")

	// The module of a build up to date is touched, so that the core takes
	// it as made.
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(module, old, old))

	_, _ = rebuild(t, harness, bld)

	info, err := os.Stat(module)
	require.NoError(t, err)
	assert.True(t, info.ModTime().After(old.Add(time.Minute)))

	writeFile(t, harness, "b.c", "/* b.c changed */\n")

	sources, linked = rebuild(t, harness, bld)
	assert.Equal(t, []string{"b.c"}, sources, "a source changed")
	assert.True(t, linked, "an object changed")

	// A header the depfile of a.c lists.
	writeFile(t, harness, "inc/a.h", "#define A 1\n")
	writeFile(t, harness, "a.c.deps", "inc/a.h")
	writeFile(t, harness, "a.c", "/* a.c includes a.h */\n")

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c"}, sources)

	sources, _ = rebuild(t, harness, bld)
	assert.Empty(t, sources)

	writeFile(t, harness, "inc/a.h", "#define A 2\n")

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c"}, sources, "a header changed")

	require.NoError(t, os.Remove(filepath.Join(harness.Context.Dirs.Cwd, "inc/a.h")))
	require.NoError(t, os.Remove(filepath.Join(harness.Context.Dirs.Cwd, "a.c.deps")))

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c"}, sources, "a header gone")

	require.NoError(t, os.Remove(module))

	sources, linked = rebuild(t, harness, bld)
	assert.Empty(t, sources)
	assert.True(t, linked, "the module is gone")

	require.NoError(t, os.WriteFile(module, []byte("changed"), 0o600))

	_, linked = rebuild(t, harness, bld)
	assert.True(t, linked, "the module changed")

	object := filepath.Join(work, "obj", objectName("sub/c.cpp"))
	require.NoError(t, os.WriteFile(object, []byte("broken"), 0o600))

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"sub/c.cpp"}, sources, "an object changed")

	require.NoError(t, os.Remove(object+".json"))

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"sub/c.cpp"}, sources, "a record gone")

	require.NoError(t, os.WriteFile(object+".json", []byte("{"), 0o600))

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"sub/c.cpp"}, sources, "a record broken")

	harness.Context.Fresh = true

	sources, linked = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c", "b.c", "sub/c.cpp"}, sources, "fresh")
	assert.True(t, linked, "fresh")

	harness.Context.Fresh = false
	harness.Context.Env["CPLUS_INCLUDE_PATH"] = "/opt/include"

	// The object compiled again is the one it was: the module is not
	// linked again.
	sources, linked = rebuild(t, harness, bld)
	assert.Equal(t, []string{"sub/c.cpp"}, sources, "the include path of C++")
	assert.False(t, linked)

	harness.Context.Env["C_INCLUDE_PATH"] = "/opt/include"

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c", "b.c"}, sources, "the include path of C")

	harness.Context.Env["CPATH"] = "/opt/include"

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c", "b.c", "sub/c.cpp"}, sources, "the include path of both")

	harness.Context.Env["OBJC_INCLUDE_PATH"] = "/opt/include"

	sources, _ = rebuild(t, harness, bld)
	assert.Empty(t, sources, "the include path of Objective-C")

	harness.Context.Env["TMPDIR"] = harness.Context.Dirs.Work

	sources, linked = rebuild(t, harness, bld)
	assert.Empty(t, sources, "TMPDIR is of no key")
	assert.False(t, linked, "TMPDIR is of no key")
}

// TestCompilerWarnings checks that what the compiler of a compile that
// succeeds prints is shown, once an object, with its source, in the order of
// the sources, and shown again when the object is taken from the cache.
func TestCompilerWarnings(t *testing.T) {
	t.Parallel()

	harness, bld := cachedHarness(t, "a.c", "b.c", "c.c")

	writeFile(t, harness, "c.c.warn", "c.c:1:1: warning: first\nc.c:2:1: warning: second\n")
	writeFile(t, harness, "a.c.warn", "a.c:3:5: warning: unused variable 'x'\n")

	shown := []string{
		"compiling a.c:\na.c:3:5: warning: unused variable 'x'",
		"compiling c.c:\nc.c:1:1: warning: first\nc.c:2:1: warning: second",
	}

	sources, _ := rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c", "b.c", "c.c"}, sources)
	assert.Equal(t, shown, harness.Warnings())

	// Nothing is compiled again, and the warnings are shown all the same.
	sources, _ = rebuild(t, harness, bld)
	assert.Empty(t, sources)
	assert.Equal(t, slices.Concat(shown, shown), harness.Warnings())

	// The source fixed, its warning is gone.
	require.NoError(t, os.Remove(filepath.Join(harness.Context.Dirs.Cwd, "a.c.warn")))
	writeFile(t, harness, "a.c", "/* a.c fixed */\n")

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.c"}, sources)
	assert.Equal(t, slices.Concat(shown, shown, shown[1:]), harness.Warnings())
}

// TestCacheFlags checks that a change of the flags of one language compiles
// the objects of that language alone, and one of the link links alone.
func TestCacheFlags(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		rest    map[string]any
		environ map[string]string
		want    []string
		linked  bool
	}{
		{"cflags", map[string]any{"cflags": []any{"-Wall"}}, nil, []string{"a.c", "d.S"}, true},
		{"cxxflags", map[string]any{"cxxflags": []any{"-Wall"}}, nil, []string{"b.cc"}, true},
		{"defines", map[string]any{"defines": []any{"X"}}, nil,
			[]string{"a.c", "b.cc", "d.S"}, true},
		{"ldflags", map[string]any{"ldflags": []any{"-s"}}, nil, nil, true},
		{"CFLAGS", nil, map[string]string{"CC": "fakecc", "CXX": "fakec++", "CFLAGS": "-g"},
			[]string{"a.c", "d.S"}, true},
		{"CXXFLAGS", nil, map[string]string{"CC": "fakecc", "CXX": "fakec++", "CXXFLAGS": "-g"},
			[]string{"b.cc"}, true},
		{"LDFLAGS", nil, map[string]string{"CC": "fakecc", "CXX": "fakec++", "LDFLAGS": "-s"},
			nil, true},
		{"CXX", nil, map[string]string{"CC": "fakecc", "CXX": "ccache fakec++"},
			[]string{"b.cc"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness, bld := cachedHarness(t, "a.c", "b.cc", "d.S")

			sources, _ := rebuild(t, harness, bld)
			require.Len(t, sources, 3)

			// The core builds a block that changed afresh, Fresh and its
			// work directory emptied; one of the same block whose
			// environment changed it builds as it is. Both are run in the
			// same work directory here, so that the test shows what the
			// backend itself tells apart.
			changed := changedBuild(t, harness, test.rest, test.environ, "a.c", "b.cc", "d.S")
			sources, linked := rebuild(t, harness, changed)
			assert.Equal(t, test.want, sources)
			assert.Equal(t, test.linked, linked)
		})
	}
}

// changedBuild returns the build of the block of cachedHarness with rest,
// in the environment tt runs in of environ, for harness.
func changedBuild(t *testing.T, harness *buildtest.Harness, rest map[string]any,
	environ map[string]string, sources ...string,
) ccBuild {
	t.Helper()

	if environ == nil {
		environ = fakeCompilers()
	}

	block := map[string]any{"module": "m", "sources": toAny(sources)}

	maps.Copy(block, rest)

	validated, err := buildtest.Validate(C(), component(), buildtest.Block{
		Common: build.Common{Cwd: "", Env: nil, Output: nil, Static: false}, Incremental: false,
		Fields: block,
	})
	require.NoError(t, err)

	bld, ok := validated.(ccBuild)
	require.True(t, ok)

	if strings.HasPrefix(environ["CXX"], "ccache ") {
		writeTool(t, harness, "ccache", "#!/bin/sh\nexec \"$@\"\n")
	}

	bld.lookupEnv = func(key string) (string, bool) {
		value, set := environ[key]

		return value, set
	}

	return bld
}

// writeTool writes the program name of body into the bin directory of the
// project of harness.
func writeTool(t *testing.T, harness *buildtest.Harness, name, body string) {
	t.Helper()

	writeProgram(t, filepath.Join(harness.Context.Dirs.Tools, name), body)
}

// writeProgram writes the program of body at path. It is copied into place
// with no fork of the parallel tests in between (build.CopyFile): a fork
// would hold it open for writing, and its first run, a probe of the
// compiler say, would fail with "text file busy" - and the probe that
// failed would make another compiler of the one of the next build.
func writeProgram(t *testing.T, path, body string) {
	t.Helper()

	script := filepath.Join(t.TempDir(), filepath.Base(path))

	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))
	require.NoError(t, build.CopyFile(path, script))
}

// TestCacheCompilerChanged checks that a compiler that changed, its program
// or its version, compiles everything of it again.
func TestCacheCompilerChanged(t *testing.T) {
	t.Parallel()

	harness, bld := cachedHarness(t, "a.c", "b.cc")

	_, _ = rebuild(t, harness, bld)

	// The fake C++ compiler says another version: the program is another
	// file too.
	writeTool(t, harness, "fakec++", strings.Replace(fakeCC, "#!/bin/sh\n",
		"#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'fake 2'; exit 0; fi\n", 1))

	sources, linked := rebuild(t, harness, bld)
	assert.Equal(t, []string{"b.cc"}, sources)
	assert.True(t, linked, "the C++ compiler links")
}

// TestCacheAssembler checks that assembler as it is, of no depfile, is
// cached by its source, and that a depfile missing of a C source leaves its
// object uncached.
func TestCacheAssembler(t *testing.T) {
	t.Parallel()

	harness, bld := cachedHarness(t, "a.s", "b.c")

	// The C compiler that writes no depfile.
	writeTool(t, harness, "fakecc", strings.Replace(fakeCC, `if [ -n "$dep" ]; then`,
		`if [ -n "$dep" ] && [ "${src##*.}" != c ] && [ "${src##*.}" != s ]; then`, 1))

	sources, _ := rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.s", "b.c"}, sources)

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"b.c"}, sources, "the object of b.c has no record")

	writeFile(t, harness, "a.s", "# changed\n")

	sources, _ = rebuild(t, harness, bld)
	assert.Equal(t, []string{"a.s", "b.c"}, sources)
}
