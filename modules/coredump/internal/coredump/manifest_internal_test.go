package coredump

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vendorFile is where tt records where the copies of the extensions it
// embeds come from: data of tt, no part of the schema of the manifest.
const vendorFile = "vendor.json"

// vendorRecord is the content of vendorFile.
type vendorRecord struct {
	// Files are the copies, one entry each.
	Files []vendoredFile `json:"files"`
}

// vendoredFile is where a copy comes from.
type vendoredFile struct {
	// File is the name of the copy in the directory of the extensions.
	File string `json:"file"`
	// Repo is the GitHub repository of the file, owner/name.
	Repo string `json:"repo"`
	// Path is the path of the file in the repository.
	Path string `json:"path"`
	// Ref is the ref the file was taken at, a tag or a commit.
	Ref string `json:"ref"`
	// Commit is the full commit of the ref.
	Commit string `json:"commit"`
	// SHA256 is the checksum of the copy, in hex.
	SHA256 string `json:"sha256"`
	// Note says, if anything, what the ref is of.
	Note string `json:"note"`
}

// readVendorRecord returns the vendorFile of the copies of the upstream
// extensions tt embeds (see readVendorRecordOf).
func readVendorRecord(t *testing.T) vendorRecord {
	t.Helper()

	return readVendorRecordOf(t, embeddedSet)
}

// readVendorRecordOf returns the vendorFile of set, which must hold no field
// the test does not know.
func readVendorRecordOf(t *testing.T, set extensionSet) vendorRecord {
	t.Helper()

	data, err := fs.ReadFile(set.files, set.dir+"/"+vendorFile)
	require.NoError(t, err)

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var record vendorRecord

	require.NoError(t, decoder.Decode(&record))

	return record
}

// gdbParameterPattern matches a parameter of gdb a Python extension
// registers, the way the extensions of Tarantool and LuaJIT declare one: a
// subclass of gdb.Parameter whose first line names it, as name or, in
// tarantool_gdb.py of tarantool-gdb-tools, as param_name.
var gdbParameterPattern = regexp.MustCompile(
	`(?m)^class \w+\(gdb\.Parameter\):\n[ \t]+(?:param_)?name = ['"]([^'"]+)['"]`)

// registeredParameters returns the names of the parameters of gdb content,
// an extension, registers.
func registeredParameters(content string) []string {
	matches := gdbParameterPattern.FindAllStringSubmatch(content, -1)
	names := make([]string, 0, len(matches))

	for _, match := range matches {
		names = append(names, match[1])
	}

	return names
}

// manifestFiles returns the files of entries, in order.
func manifestFiles(entries []manifestEntry) []string {
	files := make([]string, 0, len(entries))

	for _, entry := range entries {
		files = append(files, entry.File)
	}

	return files
}

// TestEmbeddedManifest checks that the manifest of the copies tt embeds
// names every file of their directory but itself and vendor.json, and only
// those, in the order the copies were loaded in before it, and that every
// command it lists is a name the file has. The one directory under theirs
// is that of the extensions of tarantool-gdb-tools, which has a manifest
// and a record of its own (see TestGDBToolsVendored): any other directory,
// as any other file, would be embedded with them and loaded by nothing.
func TestEmbeddedManifest(t *testing.T) {
	t.Parallel()

	entries, err := embeddedManifest()
	require.NoError(t, err)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, manifestFiles(entries))

	dir, err := extensions.ReadDir(extensionsDir)
	require.NoError(t, err)

	var present []string

	for _, entry := range dir {
		switch {
		case entry.IsDir() && path.Join(extensionsDir, entry.Name()) == gdbToolsDir:
		case entry.Name() != manifestFile && entry.Name() != vendorFile:
			present = append(present, entry.Name())
		}
	}

	assert.ElementsMatch(t, present, manifestFiles(entries), "the manifest and the directory")

	for _, entry := range entries {
		assert.NotEmpty(t, entry.Debuggers, entry.File)
		assert.Equal(t, versionRange{Min: embeddedTarantool, Max: embeddedTarantool},
			entry.Tarantool, entry.File)
		assert.Equal(t, []string{"x86_64", "aarch64"}, entry.Arch, entry.File)
		require.NotEmpty(t, entry.Commands, entry.File)

		content := embeddedContent(t, entry.File)

		for _, command := range entry.Commands {
			assert.True(t, strings.Contains(content, "'"+command+"'") ||
				strings.Contains(content, `"`+command+`"`), "%s registers %s", entry.File, command)
		}

		assert.ElementsMatch(t, registeredParameters(content), entry.Parameters,
			"the parameters %s registers", entry.File)
	}
}

// TestManifestErrorNames checks that an error of a manifest that does not
// read names it by its path under the directory of the extensions: the
// manifest of the copies of the upstream extensions by its name alone, as
// before there was another set, that of tarantool-gdb-tools under the
// directory of the set.
func TestManifestErrorNames(t *testing.T) {
	t.Parallel()

	files := fstest.MapFS{
		extensionsDir + "/" + manifestFile: {Data: []byte(`{"schema": 0}`)},
		gdbToolsDir + "/" + manifestFile:   {Data: []byte(`{"schema": 0}`)},
	}

	for dir, want := range map[string]string{
		extensionsDir: "embedded extensions.json: ",
		gdbToolsDir:   "embedded gdb-tools/extensions.json: ",
	} {
		_, err := extensionSet{files: files, dir: dir}.manifest()
		require.ErrorIs(t, err, errManifestSchema, dir)
		assert.EqualError(t, err, want+"unknown schema of the extension manifest: 0, this tt "+
			"reads 1", dir)
	}
}

// TestVendoredCopies checks that vendor.json tells where every copy of the
// manifest comes from, and only those, and that each copy is the file it
// names: its checksum, and the commit and the version the warnings of a
// copy name.
func TestVendoredCopies(t *testing.T) {
	t.Parallel()

	entries, err := embeddedManifest()
	require.NoError(t, err)

	record := readVendorRecord(t)
	vendored := make([]string, 0, len(record.Files))

	for _, file := range record.Files {
		vendored = append(vendored, file.File)

		data, err := extensions.ReadFile(extensionsDir + "/" + file.File)
		require.NoError(t, err)

		sum := sha256.Sum256(data)
		assert.Equal(t, file.SHA256, hex.EncodeToString(sum[:]), file.File)
		assert.NotEmpty(t, file.Repo, file.File)
		assert.Equal(t, file.File, filepath.Base(file.Path), file.File)
		assert.NotEmpty(t, file.Ref, file.File)
		assert.Regexp(t, fullCommitPattern, file.Commit, file.File)
	}

	assert.ElementsMatch(t, manifestFiles(entries), vendored, "the manifest and vendor.json")

	for _, file := range record.Files {
		switch file.File {
		case luajitDbgName:
			assert.Equal(t, "tarantool/luajit", file.Repo)
			assert.True(t, strings.HasPrefix(file.Commit, embeddedLuaJIT), file.Commit)
		case tarantoolGDBName:
			assert.Equal(t, "tarantool/tarantool", file.Repo)
			assert.Equal(t, embeddedTarantool, file.Ref)
		}
	}
}

// The universal extensions of tarantool-gdb-tools tt embeds, by the names of
// their files, and the repository.
const (
	gdbToolsLuaJIT    = "luajit_gdb.py"
	gdbToolsTarantool = "tarantool_gdb.py"
	gdbToolsBacktrace = "luajit_backtrace_gdb.py"
	gdbToolsGcore     = "gcore_pages_gdb.py"
	gdbToolsRepo      = "buyemune-sys/tarantool-gdb-tools"
)

// gdbToolsFiles are the license files of tarantool-gdb-tools, which a copy
// of its extensions carries along: LICENSE, the BSD 2-Clause License of the
// repository, asks a copy to keep it, and NOTICE has the licenses of the
// extensions that come from LuaJIT and Tarantool.
var gdbToolsFiles = []string{"LICENSE", "NOTICE"}

// TestGDBToolsVendored checks that vendor.json of the extensions of
// tarantool-gdb-tools names every other file of their directory, and only
// those, the extensions, their manifest and the license files, each the
// file of the root of the repository at one full commit, with its
// checksum: the files are copies, byte for byte, the manifest too.
//
// To pin another commit of tarantool-gdb-tools, run this from the root of
// tt: it replaces the files of the directory with those of the commit,
// writes vendor.json of them and stages it all, the files gone too, for
// git commit:
//
//	clone=<a clone of buyemune-sys/tarantool-gdb-tools> rev=<a commit of it>
//	commit=$(git -C "$clone" rev-parse --verify "$rev^{commit}")
//	dir=modules/coredump/internal/coredump/extensions/gdb-tools
//	rm -f -- "${dir:?}"/*
//	files=$(git -C "$clone" cat-file blob "$commit:extensions.json" |
//		jq -r '.extensions[].file')
//	for file in extensions.json $files LICENSE NOTICE; do
//		git -C "$clone" cat-file blob "$commit:$file" > "$dir/$file"
//	done
//	(cd "$dir" && sha256sum extensions.json $files LICENSE NOTICE) |
//		jq -nR --arg commit "$commit" '{files: [inputs | split("  ") | {
//			file: .[1], repo: "buyemune-sys/tarantool-gdb-tools", path: .[1],
//			ref: $commit, commit: $commit, sha256: .[0]}]}' > "$dir/vendor.json"
//	git add -A "$dir"
//
// Then cd modules/coredump && go test ./... checks the copy. A commit that
// adds, removes or renames an extension changes what inspect loads: the
// tests that name the extensions, TestGDBToolsManifest among them, fail
// until they name the new ones.
func TestGDBToolsVendored(t *testing.T) {
	t.Parallel()

	record := readVendorRecordOf(t, gdbToolsSet)
	vendored := make([]string, 0, len(record.Files))
	commits := map[string]bool{}

	for _, file := range record.Files {
		vendored = append(vendored, file.File)

		data, err := fs.ReadFile(gdbToolsSet.files, gdbToolsSet.dir+"/"+file.File)
		require.NoError(t, err)

		sum := sha256.Sum256(data)
		assert.Equal(t, file.SHA256, hex.EncodeToString(sum[:]), file.File)
		assert.Equal(t, gdbToolsRepo, file.Repo, file.File)
		assert.Equal(t, file.File, file.Path, file.File)
		assert.Regexp(t, fullCommitPattern, file.Commit, file.File)
		assert.Equal(t, file.Commit, file.Ref, file.File)

		commits[file.Commit] = true
	}

	assert.Len(t, commits, 1, "the files are of one commit")

	dir, err := fs.ReadDir(gdbToolsSet.files, gdbToolsSet.dir)
	require.NoError(t, err)

	var present []string

	for _, entry := range dir {
		if entry.Name() != vendorFile {
			present = append(present, entry.Name())
		}
	}

	assert.ElementsMatch(t, present, vendored, "the directory and vendor.json")

	entries, err := gdbToolsSet.manifest()
	require.NoError(t, err)
	assert.ElementsMatch(t, append(append([]string{manifestFile}, gdbToolsFiles...),
		manifestFiles(entries)...), vendored, "the manifest and vendor.json")
}

// TestGDBToolsManifest checks that the manifest of the extensions of
// tarantool-gdb-tools reads, lists the Python files of their directory, and
// only those, lj-bt after the extension of LuaJIT, whose helpers it takes,
// and that each entry says the debuggers it loads in, the versions and
// the architectures it was checked on, in the terms inspect compares a
// core with, and the commands and the parameters the file registers: every
// command is a name the file has, and the parameters are those its
// gdb.Parameter subclasses name.
func TestGDBToolsManifest(t *testing.T) {
	t.Parallel()

	entries, err := gdbToolsSet.manifest()
	require.NoError(t, err)
	assert.Equal(t, []string{gdbToolsLuaJIT, gdbToolsTarantool, gdbToolsBacktrace, gdbToolsGcore},
		manifestFiles(entries))

	dir, err := fs.ReadDir(gdbToolsSet.files, gdbToolsSet.dir)
	require.NoError(t, err)

	var scripts []string

	for _, entry := range dir {
		if path.Ext(entry.Name()) == ".py" {
			scripts = append(scripts, entry.Name())
		}
	}

	assert.ElementsMatch(t, scripts, manifestFiles(entries), "the manifest and the directory")

	for _, entry := range entries {
		assert.NotEmpty(t, entry.Debuggers, entry.File)
		assert.Subset(t, []string{debuggerGDB, debuggerLLDB}, entry.Debuggers, entry.File)
		assert.NotEmpty(t, entry.Tarantool.Min, entry.File)
		assert.NotEmpty(t, entry.Tarantool.Max, entry.File)
		assert.NotEmpty(t, entry.Arch, entry.File)
		assert.Subset(t, []string{"x86_64", "aarch64"}, entry.Arch, entry.File)
		require.NotEmpty(t, entry.Commands, entry.File)

		data, err := fs.ReadFile(gdbToolsSet.files, gdbToolsSet.dir+"/"+entry.File)
		require.NoError(t, err)

		content := string(data)

		for _, command := range entry.Commands {
			assert.True(t, strings.Contains(content, "'"+command+"'") ||
				strings.Contains(content, `"`+command+`"`), "%s registers %s", entry.File, command)
		}

		assert.ElementsMatch(t, registeredParameters(content), entry.Parameters,
			"the parameters %s registers", entry.File)

		if entry.File == gdbToolsBacktrace {
			assert.Equal(t, []string{gdbToolsLuaJIT}, entry.After, entry.File)
		} else {
			assert.Empty(t, entry.After, entry.File)
		}
	}
}

// TestEmbeddedExtensions checks that the copies inspect loads for artefacts
// without a version are the extensions of the manifest, in its order, and
// not the other files of the directory.
func TestEmbeddedExtensions(t *testing.T) {
	t.Parallel()

	exts, err := embeddedExtensions()
	require.NoError(t, err)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, names(exts))
	assert.Equal(t, map[string]string{
		luajitDbgName:    embeddedContent(t, luajitDbgName),
		tarantoolGDBName: embeddedContent(t, tarantoolGDBName),
	}, contents(exts))
}

// TestWriteExtensions checks that pack puts the extensions of the manifest
// into the archive, in its order, and not the other files of the
// directory.
func TestWriteExtensions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	paths, err := writeExtensions(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, luajitDbgName), filepath.Join(dir, tarantoolGDBName),
	}, paths)

	written, err := os.ReadDir(dir)
	require.NoError(t, err)

	files := make([]string, 0, len(written))

	for _, entry := range written {
		files = append(files, entry.Name())
	}

	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, files)

	data, err := os.ReadFile(filepath.Join(dir, tarantoolGDBName))
	require.NoError(t, err)
	assert.Equal(t, embeddedContent(t, tarantoolGDBName), string(data))
}

// TestParseManifest checks that every field of an entry is read, that a
// field the reader does not know is ignored, and that the entries come in
// their load order: each after the files of its after, and otherwise in
// the order of the manifest.
func TestParseManifest(t *testing.T) {
	t.Parallel()

	entries, err := parseManifest([]byte(`{
		"schema": 1,
		"generated": "by a newer gdb-tools",
		"extensions": [
			{"file": "d.py", "after": ["c.py", "a.py"]},
			{"file": "c.py", "after": ["b.py"], "since": "3.10"},
			{"file": "a.py", "debuggers": ["gdb", "lldb"], "after": [],
			 "tarantool": {"min": "3.3.0", "max": "3.9.0", "build": "any"},
			 "arch": ["x86_64", "aarch64"], "commands": ["a-one", "info a-two"],
			 "parameters": ["print a-three"]},
			{"file": "b.py"},
			{"file": "e.py"}
		]
	}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"a.py", "b.py", "c.py", "d.py", "e.py"}, manifestFiles(entries))
	assert.Equal(t, manifestEntry{
		File:       "a.py",
		Debuggers:  []string{"gdb", "lldb"},
		After:      []string{},
		Tarantool:  versionRange{Min: "3.3.0", Max: "3.9.0"},
		Arch:       []string{"x86_64", "aarch64"},
		Commands:   []string{"a-one", "info a-two"},
		Parameters: []string{"print a-three"},
	}, entries[0])
	assert.Nil(t, entries[1].Parameters, "a manifest may leave the parameters out")

	// With no after the order is the one of the manifest.
	entries, err = parseManifest([]byte(`{"schema": 1, "extensions": [
		{"file": "z.py"}, {"file": "a.py"}, {"file": "m.py"}
	]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"z.py", "a.py", "m.py"}, manifestFiles(entries))
}

// TestParseManifestRejects checks the manifests the reader refuses.
func TestParseManifestRejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		manifest string
		err      error
		message  string
	}{
		{
			name:     "newer schema",
			manifest: `{"schema": 2, "extensions": []}`,
			err:      errManifestSchema,
			message:  "unknown schema of the extension manifest: 2, this tt reads 1",
		},
		{
			name:     "no schema",
			manifest: `{"extensions": [{"file": "a.py"}]}`,
			err:      errManifestSchema,
			message:  "unknown schema of the extension manifest: 0",
		},
		{
			name:     "unknown file in after",
			manifest: `{"schema": 1, "extensions": [{"file": "a.py", "after": ["b.py"]}]}`,
			err:      errManifestAfter,
			message:  `a.py is to load after "b.py"`,
		},
		{
			name: "cycle",
			manifest: `{"schema": 1, "extensions": [{"file": "a.py"},
				{"file": "b.py", "after": ["c.py"]}, {"file": "c.py", "after": ["d.py"]},
				{"file": "d.py", "after": ["b.py", "a.py"]}]}`,
			err:     errLoadCycle,
			message: "the extensions load after each other: b.py, c.py, d.py",
		},
		{
			name:     "after itself",
			manifest: `{"schema": 1, "extensions": [{"file": "a.py", "after": ["a.py"]}]}`,
			err:      errLoadCycle,
			message:  "the extensions load after each other: a.py",
		},
		{
			name:     "a file twice",
			manifest: `{"schema": 1, "extensions": [{"file": "a.py"}, {"file": "a.py"}]}`,
			err:      errManifestFile,
			message:  `bad file in the extension manifest: "a.py"`,
		},
		{
			name:     "no file",
			manifest: `{"schema": 1, "extensions": [{"debuggers": ["gdb"]}]}`,
			err:      errManifestFile,
			message:  `bad file in the extension manifest: ""`,
		},
		{
			name:     "a file in another directory",
			manifest: `{"schema": 1, "extensions": [{"file": "../a.py"}]}`,
			err:      errManifestFile,
			message:  `"../a.py"`,
		},
		{
			name:     "a directory",
			manifest: `{"schema": 1, "extensions": [{"file": ".."}]}`,
			err:      errManifestFile,
			message:  `".."`,
		},
		{
			name:     "no JSON",
			manifest: `{"schema": 1, "extensions": [`,
			err:      nil,
			message:  "cannot read the extension manifest",
		},
		{
			name:     "a field of a wrong type",
			manifest: `{"schema": 1, "extensions": [{"file": "a.py", "after": "b.py"}]}`,
			err:      nil,
			message:  "cannot read the extension manifest",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			entries, err := parseManifest([]byte(testCase.manifest))
			require.Error(t, err)
			assert.Nil(t, entries)

			if testCase.err != nil {
				require.ErrorIs(t, err, testCase.err)
			}

			assert.ErrorContains(t, err, testCase.message)
		})
	}
}
