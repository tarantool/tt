package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// editPrelude and editTables are the minimum a manifest needs to parse and
// validate, split so a fixture can put root-level dotted keys between them.
//
// The split matters: a dotted key is only a *root* key while no table header
// has been seen yet, so `dependencies.foo = "1"` written after [platform] is
// platform.dependencies.foo and not a dependency at all.
const (
	editPrelude = `manifest_version = "0.1"
`
	editTables = `
[package]
name = "demo"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"
`
	editHeader = editPrelude + editTables
)

// reparse runs edited bytes back through the real reader. Every mutation is
// checked with it: an editor that emits TOML the manifest reader rejects has
// destroyed a file the user hand-wrote, which is the worst failure this
// package has.
func reparse(t *testing.T, src []byte) *manifest.Manifest {
	t.Helper()

	mfst, warnings, err := manifest.ParseManifest(src)
	require.NoError(t, err, "edited manifest must still parse:\n%s", src)
	require.Empty(t, warnings)

	_, err = mfst.Validate()
	require.NoError(t, err, "edited manifest must still validate:\n%s", src)

	return mfst
}

// edit builds an Editor over the fixture and fails the test if the fixture
// itself is unusable.
func edit(t *testing.T, src string) *manifest.Editor {
	t.Helper()

	editor, err := manifest.NewEditor([]byte(src))
	require.NoError(t, err)

	return editor
}

// TestEditorAddToExistingTable checks that a new dependency lands after the
// last entry of the section rather than at the end of the file or in
// alphabetical order.
func TestEditorAddToExistingTable(t *testing.T) {
	t.Parallel()

	src := editHeader + `
[dependencies]
zzz = ">=1.0.0"
aaa = ">=2.0.0"

[dev_dependencies]
luatest = ">=1.0.0"
`
	editor := edit(t, src)

	change, err := editor.SetDependency(manifest.TableDependencies, "checks", ">=3.1.0")
	require.NoError(t, err)
	assert.False(t, change.Existed)
	assert.Empty(t, change.Previous)

	assert.Equal(t, editHeader+`
[dependencies]
zzz = ">=1.0.0"
aaa = ">=2.0.0"
checks = ">=3.1.0"

[dev_dependencies]
luatest = ">=1.0.0"
`, string(editor.Bytes()))

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=3.1.0", mfst.Dependencies["checks"].Version)
	assert.Equal(t, "registry", mfst.Dependencies["checks"].Source)
	assert.Len(t, mfst.Dependencies, 3)
}

// TestEditorAddSkipsSubTables checks the insertion point steps over a
// dependency sub-table: a key appended after [dependencies.helper] would
// silently become a field of helper.
func TestEditorAddSkipsSubTables(t *testing.T) {
	t.Parallel()

	src := editHeader + `
[dependencies]
zzz = ">=1.0.0"

[dependencies.helper]
source = "path"
path = "../helper"
`
	editor := edit(t, src)

	_, err := editor.SetDependency(manifest.TableDependencies, "checks", ">=3.1.0")
	require.NoError(t, err)

	assert.Equal(t, editHeader+`
[dependencies]
zzz = ">=1.0.0"
checks = ">=3.1.0"

[dependencies.helper]
source = "path"
path = "../helper"
`, string(editor.Bytes()))

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=3.1.0", mfst.Dependencies["checks"].Version)
	assert.Equal(t, "../helper", mfst.Dependencies["helper"].Path)
}

// TestEditorAddToEmptySection checks a section that only has a header.
func TestEditorAddToEmptySection(t *testing.T) {
	t.Parallel()

	src := editHeader + `
[dependencies]

[dev_dependencies]
luatest = ">=1.0.0"
`
	editor := edit(t, src)

	_, err := editor.SetDependency(manifest.TableDependencies, "checks", ">=3.1.0")
	require.NoError(t, err)

	assert.Equal(t, editHeader+`
[dependencies]
checks = ">=3.1.0"

[dev_dependencies]
luatest = ">=1.0.0"
`, string(editor.Bytes()))

	reparse(t, editor.Bytes())
}

// TestEditorAddCreatesTable covers the absent-table case for both tables: a
// new section is appended at the end, one blank line down.
func TestEditorAddCreatesTable(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		table manifest.DepTable
		want  string
	}{
		{
			name:  "dependencies",
			table: manifest.TableDependencies,
			want:  editHeader + "\n[dependencies]\nchecks = \">=3.1.0\"\n",
		},
		{
			name:  "dev_dependencies",
			table: manifest.TableDevDependencies,
			want:  editHeader + "\n[dev_dependencies]\nchecks = \">=3.1.0\"\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			editor := edit(t, editHeader)

			change, err := editor.SetDependency(testCase.table, "checks", ">=3.1.0")
			require.NoError(t, err)
			assert.False(t, change.Existed)

			assert.Equal(t, testCase.want, string(editor.Bytes()))

			mfst := reparse(t, editor.Bytes())
			if testCase.table == manifest.TableDependencies {
				assert.Equal(t, ">=3.1.0", mfst.Dependencies["checks"].Version)
			} else {
				assert.Equal(t, ">=3.1.0", mfst.DevDependencies["checks"].Version)
			}
		})
	}
}

// TestEditorAddAfterRootDottedKeys checks the case TOML makes special: a table
// defined by dotted keys at the root cannot be reopened with a [header], so a
// new entry has to be written as one more dotted key.
func TestEditorAddAfterRootDottedKeys(t *testing.T) {
	t.Parallel()

	src := editPrelude + `dependencies.luasocket = ">=3.0.0"
` + editTables
	editor := edit(t, src)

	_, err := editor.SetDependency(manifest.TableDependencies, "checks", ">=3.1.0")
	require.NoError(t, err)

	assert.Equal(t, editPrelude+`dependencies.luasocket = ">=3.0.0"
dependencies.checks = ">=3.1.0"
`+editTables, string(editor.Bytes()))

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=3.1.0", mfst.Dependencies["checks"].Version)
	assert.Equal(t, ">=3.0.0", mfst.Dependencies["luasocket"].Version)
}

// preserveFixture exercises every construct the editor has to walk past
// untouched: a header comment, comments between and inside sections, a
// trailing same-line comment on a neighbouring entry, an inline table, a
// sub-table and aligned-with-spaces keys.
const preserveFixture = `# app.manifest.toml for demo
# second header line

manifest_version = "0.1"

[package]
name        = "demo"
description = "a demo package"   # keep me

# --- what demo needs to run ---
[dependencies]
luasocket   = ">=3.0.0,<4.0.0"   # the socket library
checks      = ">=3.1.0"
inline      = { source = "registry", version = ">=1.0.0", kind = "library" }

# helper lives in the sibling directory
[dependencies.local-helper]
source = "path"
path   = "../helper"

# --- and to test itself ---
[dev_dependencies]
luatest = ">=1.0.0"

[platform]
tarantool = ">=3.0.0"
tt        = ">=3.0.0"
`

// TestEditorPreservesEverythingElse is the headline case: after rewriting one
// constraint the file must differ from the original in exactly that one token.
func TestEditorPreservesEverythingElse(t *testing.T) {
	t.Parallel()

	editor := edit(t, preserveFixture)

	change, err := editor.SetDependency(manifest.TableDependencies, "checks", ">=4.0.0")
	require.NoError(t, err)
	assert.True(t, change.Existed)
	assert.Equal(t, ">=3.1.0", change.Previous)

	want := strings.Replace(preserveFixture,
		`checks      = ">=3.1.0"`, `checks      = ">=4.0.0"`, 1)
	require.NotEqual(t, preserveFixture, want, "the fixture must contain the edited line")
	assert.Equal(t, want, string(editor.Bytes()))

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=4.0.0", mfst.Dependencies["checks"].Version)
	assert.Equal(t, ">=3.0.0,<4.0.0", mfst.Dependencies["luasocket"].Version)
	assert.Equal(t, "a demo package", mfst.Package.Description)
}

// TestEditorSetRewritesEveryForm covers the three editable declaration forms
// and the constraint each one reports as the previous value.
func TestEditorSetRewritesEveryForm(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		dep      string
		previous string
		want     string
	}{
		{
			name:     "short",
			dep:      "checks",
			previous: ">=3.1.0",
			want:     `checks      = ">=9.9.9"`,
		},
		{
			name:     "inline table",
			dep:      "inline",
			previous: ">=1.0.0",
			want: `inline      = ` +
				`{ source = "registry", version = ">=9.9.9", kind = "library" }`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			editor := edit(t, preserveFixture)

			change, err := editor.SetDependency(manifest.TableDependencies, testCase.dep, ">=9.9.9")
			require.NoError(t, err)
			assert.True(t, change.Existed)
			assert.Equal(t, testCase.previous, change.Previous)

			assert.Contains(t, string(editor.Bytes()), testCase.want)

			mfst := reparse(t, editor.Bytes())
			assert.Equal(t, ">=9.9.9", mfst.Dependencies[testCase.dep].Version)
		})
	}
}

// TestEditorSetSubTableVersion rewrites the version key of a long-form
// declaration written as its own table, and leaves the rest of the body alone.
func TestEditorSetSubTableVersion(t *testing.T) {
	t.Parallel()

	src := editHeader + `
[dependencies.metrics]
source   = "registry"
version  = ">=0.15.0"     # pinned by the ops team
registry = "https://rocks.example"
`
	editor := edit(t, src)

	change, err := editor.SetDependency(manifest.TableDependencies, "metrics", ">=1.0.0")
	require.NoError(t, err)
	assert.True(t, change.Existed)
	assert.Equal(t, ">=0.15.0", change.Previous)

	assert.Equal(t, editHeader+`
[dependencies.metrics]
source   = "registry"
version  = ">=1.0.0"     # pinned by the ops team
registry = "https://rocks.example"
`, string(editor.Bytes()))

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=1.0.0", mfst.Dependencies["metrics"].Version)
	assert.Equal(t, "https://rocks.example", mfst.Dependencies["metrics"].Registry)
}

// TestEditorSetDottedKeyVersion rewrites a version declared as a root-level
// dotted key.
func TestEditorSetDottedKeyVersion(t *testing.T) {
	t.Parallel()

	src := editPrelude + `dependencies.metrics.source = "registry"
dependencies.metrics.version = ">=0.15.0"
` + editTables
	editor := edit(t, src)

	change, err := editor.SetDependency(manifest.TableDependencies, "metrics", ">=1.0.0")
	require.NoError(t, err)
	assert.Equal(t, ">=0.15.0", change.Previous)

	assert.Equal(t, editPrelude+`dependencies.metrics.source = "registry"
dependencies.metrics.version = ">=1.0.0"
`+editTables, string(editor.Bytes()))

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=1.0.0", mfst.Dependencies["metrics"].Version)
}

// TestEditorRefusesVersionlessForms checks the refusals: setting a constraint
// on a declaration that has no version key is contradictory (a path dependency
// has no version to pin), so the editor names the dependency and stops instead
// of inventing a place for the key.
func TestEditorRefusesVersionlessForms(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		src  string
	}{
		{
			name: "path sub-table",
			src: editHeader + "\n[dependencies.helper]\nsource = \"path\"\n" +
				"path   = \"../helper\"\n",
		},
		{
			name: "path inline table",
			src: editHeader + "\n[dependencies]\n" +
				"helper = { source = \"path\", path = \"../helper\" }\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			editor := edit(t, testCase.src)

			change, err := editor.SetDependency(manifest.TableDependencies, "helper", ">=1.0.0")
			require.ErrorIs(t, err, manifest.ErrUnsupportedForm)
			assert.Contains(t, err.Error(), "dependencies.helper")
			assert.False(t, change.Existed)

			assert.Equal(t, testCase.src, string(editor.Bytes()),
				"a refusal must not touch the file")
		})
	}
}

// TestEditorRefusesUneditableShapes covers the constructs the editor will not
// splice: a value that is neither a constraint nor a table, and a table nested
// below the dependency, which a removal would strand.
func TestEditorRefusesUneditableShapes(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		src  string
		dep  string
	}{
		{
			name: "array value",
			src:  editHeader + "\n[dependencies]\nweird = [\">=1.0.0\"]\n",
			dep:  "weird",
		},
		{
			name: "nested table",
			src: editHeader + "\n[dependencies.weird]\nsource = \"registry\"\n" +
				"version = \">=1.0.0\"\n\n[dependencies.weird.extra]\nkey = 1\n",
			dep: "weird",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			editor := edit(t, testCase.src)

			_, err := editor.SetDependency(manifest.TableDependencies, testCase.dep, ">=2.0.0")
			require.ErrorIs(t, err, manifest.ErrUnsupportedForm)

			_, err = editor.RemoveDependency(manifest.TableDependencies, testCase.dep)
			require.ErrorIs(t, err, manifest.ErrUnsupportedForm)

			assert.Equal(t, testCase.src, string(editor.Bytes()),
				"a refusal must not touch the file")
		})
	}
}

// TestEditorRemoveEveryForm removes each of the four declaration forms and
// checks the exact remaining bytes. The sub-table case is the interesting one:
// the whole body goes and the next header stays, together with the comment
// written above it.
func TestEditorRemoveEveryForm(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "short",
			src: editHeader + "\n[dependencies]\nchecks = \">=3.1.0\"\n" +
				"luasocket = \">=3.0.0\"\n",
			want: editHeader + "\n[dependencies]\nluasocket = \">=3.0.0\"\n",
		},
		{
			name: "inline table",
			src: editHeader + "\n[dependencies]\n" +
				"checks = { source = \"registry\", version = \">=3.1.0\" }\n" +
				"luasocket = \">=3.0.0\"\n",
			want: editHeader + "\n[dependencies]\nluasocket = \">=3.0.0\"\n",
		},
		{
			name: "sub-table stops at the next header",
			src: editHeader + "\n[dependencies.checks]\nsource = \"registry\"\n" +
				"version = \">=3.1.0\"\n\n# luasocket is separate\n" +
				"[dependencies.luasocket]\nsource = \"registry\"\nversion = \">=3.0.0\"\n",
			want: editHeader + "\n\n# luasocket is separate\n" +
				"[dependencies.luasocket]\nsource = \"registry\"\nversion = \">=3.0.0\"\n",
		},
		{
			// A blank line inside a table body is legal and common. It is not
			// the end of the declaration, so a removal that stopped there
			// would strand the keys below it under the previous table.
			name: "sub-table with a blank line in its body",
			src: editHeader + "\n[dependencies.checks]\nsource = \"registry\"\n\n" +
				"version = \">=3.1.0\"\n\n# luasocket is separate\n" +
				"[dependencies.luasocket]\nsource = \"registry\"\nversion = \">=3.0.0\"\n",
			want: editHeader + "\n\n# luasocket is separate\n" +
				"[dependencies.luasocket]\nsource = \"registry\"\nversion = \">=3.0.0\"\n",
		},
		{
			name: "root dotted key",
			src: editPrelude + "dependencies.checks = \">=3.1.0\"\n" +
				"dependencies.luasocket = \">=3.0.0\"\n" + editTables,
			want: editPrelude + "dependencies.luasocket = \">=3.0.0\"\n" + editTables,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			editor := edit(t, testCase.src)

			change, err := editor.RemoveDependency(manifest.TableDependencies, "checks")
			require.NoError(t, err)
			assert.True(t, change.Existed)

			assert.Equal(t, testCase.want, string(editor.Bytes()))

			mfst := reparse(t, editor.Bytes())
			assert.NotContains(t, mfst.Dependencies, "checks")
			assert.Contains(t, mfst.Dependencies, "luasocket")
		})
	}
}

// TestEditorRemoveKeepsLeadingComment pins the deliberate choice not to guess
// what a comment above an entry documents.
func TestEditorRemoveKeepsLeadingComment(t *testing.T) {
	t.Parallel()

	src := editHeader + `
[dependencies]
# checks validates our config
checks = ">=3.1.0"
luasocket = ">=3.0.0"
`
	editor := edit(t, src)

	_, err := editor.RemoveDependency(manifest.TableDependencies, "checks")
	require.NoError(t, err)

	assert.Equal(t, editHeader+`
[dependencies]
# checks validates our config
luasocket = ">=3.0.0"
`, string(editor.Bytes()))

	reparse(t, editor.Bytes())
}

// TestEditorRemoveLastEntryKeepsHeader documents that an emptied section is
// left in place: an empty table parses to an empty map and validates.
func TestEditorRemoveLastEntryKeepsHeader(t *testing.T) {
	t.Parallel()

	src := editHeader + "\n[dependencies]\nchecks = \">=3.1.0\"\n"
	editor := edit(t, src)

	_, err := editor.RemoveDependency(manifest.TableDependencies, "checks")
	require.NoError(t, err)
	assert.Equal(t, editHeader+"\n[dependencies]\n", string(editor.Bytes()))

	mfst := reparse(t, editor.Bytes())
	assert.Empty(t, mfst.Dependencies)
}

// TestEditorUnknownName checks the sentinel both mutations return for a name
// that is not there - Set adds instead, Remove refuses.
func TestEditorUnknownName(t *testing.T) {
	t.Parallel()

	editor := edit(t, preserveFixture)

	change, err := editor.RemoveDependency(manifest.TableDependencies, "absent")
	require.ErrorIs(t, err, manifest.ErrNoSuchDependency)
	assert.False(t, change.Existed)
	assert.Equal(t, preserveFixture, string(editor.Bytes()))

	// A dependency declared in the other table is still absent from this one.
	_, err = editor.RemoveDependency(manifest.TableDependencies, "luatest")
	require.ErrorIs(t, err, manifest.ErrNoSuchDependency)

	change, err = editor.SetDependency(manifest.TableDependencies, "absent", ">=1.0.0")
	require.NoError(t, err)
	assert.False(t, change.Existed)

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=1.0.0", mfst.Dependencies["absent"].Version)
}

// TestEditorLocate checks the report of which tables declare a name, including
// a name declared in both.
func TestEditorLocate(t *testing.T) {
	t.Parallel()

	src := editHeader + `
[dependencies]
luasocket = ">=3.0.0"
helper    = { source = "path", path = "../helper" }

[dev_dependencies]
luatest   = ">=1.0.0"
luasocket = ">=3.0.0"
`
	editor := edit(t, src)

	assert.Equal(t,
		[]manifest.DepTable{manifest.TableDependencies, manifest.TableDevDependencies},
		editor.Locate("luasocket"))
	assert.Equal(t, []manifest.DepTable{manifest.TableDependencies}, editor.Locate("helper"))
	assert.Equal(t, []manifest.DepTable{manifest.TableDevDependencies}, editor.Locate("luatest"))
	assert.Empty(t, editor.Locate("absent"))
}

// TestEditorLocateSubTableAndDotted checks Locate sees the long forms too, and
// that it reports a declaration even when it is one SetDependency would
// refuse - "already there, in a shape I cannot touch" is a different answer
// from "not there".
func TestEditorLocateSubTableAndDotted(t *testing.T) {
	t.Parallel()

	src := editPrelude + `dependencies.dotted = ">=1.0.0"
` + editTables + `
[dev_dependencies.helper]
source = "path"
path   = "../helper"
`
	editor := edit(t, src)

	assert.Equal(t, []manifest.DepTable{manifest.TableDependencies}, editor.Locate("dotted"))
	assert.Equal(t, []manifest.DepTable{manifest.TableDevDependencies}, editor.Locate("helper"))

	_, err := editor.SetDependency(manifest.TableDevDependencies, "helper", ">=1.0.0")
	require.ErrorIs(t, err, manifest.ErrUnsupportedForm)
}

// TestEditorPreservesLineEndings checks a CRLF file keeps its convention: an
// inserted line must not mix "\n" into a document written with "\r\n".
func TestEditorPreservesLineEndings(t *testing.T) {
	t.Parallel()

	src := strings.ReplaceAll(editHeader+"\n[dependencies]\nchecks = \">=3.1.0\"\n",
		"\n", "\r\n")
	editor := edit(t, src)

	_, err := editor.SetDependency(manifest.TableDependencies, "luasocket", ">=3.0.0")
	require.NoError(t, err)

	out := string(editor.Bytes())
	assert.Contains(t, out, "checks = \">=3.1.0\"\r\nluasocket = \">=3.0.0\"\r\n")
	assert.Equal(t, strings.Count(out, "\n"), strings.Count(out, "\r\n"),
		"every line break must stay a CRLF")

	reparse(t, editor.Bytes())
}

// TestEditorPreservesMissingTrailingNewline checks a file that ended without a
// line break does not silently gain one. Appending necessarily writes a break
// before the new content; what is preserved is how the file *ends*.
func TestEditorPreservesMissingTrailingNewline(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "into an existing section",
			src:  editHeader + "\n[dependencies]\nchecks = \">=3.1.0\"",
			want: editHeader + "\n[dependencies]\nchecks = \">=3.1.0\"\nluasocket = \">=3.0.0\"",
		},
		{
			name: "as a new section",
			src:  strings.TrimSuffix(editHeader, "\n"),
			want: strings.TrimSuffix(editHeader, "\n") +
				"\n\n[dependencies]\nluasocket = \">=3.0.0\"",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			editor := edit(t, testCase.src)

			_, err := editor.SetDependency(manifest.TableDependencies, "luasocket", ">=3.0.0")
			require.NoError(t, err)
			assert.Equal(t, testCase.want, string(editor.Bytes()))

			mfst := reparse(t, editor.Bytes())
			assert.Equal(t, ">=3.0.0", mfst.Dependencies["luasocket"].Version)
		})
	}
}

// TestEditorRejectsBadArguments covers the arguments no parse can rescue.
func TestEditorRejectsBadArguments(t *testing.T) {
	t.Parallel()

	editor := edit(t, preserveFixture)

	_, err := editor.SetDependency("components", "checks", ">=1.0.0")
	require.Error(t, err)

	_, err = editor.SetDependency(manifest.TableDependencies, "", ">=1.0.0")
	require.Error(t, err)

	_, err = editor.SetDependency(manifest.TableDependencies, "checks", "")
	require.Error(t, err)

	_, err = editor.RemoveDependency("components", "checks")
	require.Error(t, err)

	assert.Equal(t, preserveFixture, string(editor.Bytes()))
}

// TestNewEditorRejectsBrokenTOML checks the editor refuses to work on a source
// it cannot locate anything in.
func TestNewEditorRejectsBrokenTOML(t *testing.T) {
	t.Parallel()

	_, err := manifest.NewEditor([]byte("[dependencies\nchecks = \">=1.0.0\"\n"))
	require.Error(t, err)
}

// TestEditorSequentialEdits checks the source stays consistent across several
// edits, since each one re-indexes what the previous one produced.
func TestEditorSequentialEdits(t *testing.T) {
	t.Parallel()

	editor := edit(t, preserveFixture)

	_, err := editor.SetDependency(manifest.TableDependencies, "checks", ">=4.0.0")
	require.NoError(t, err)

	_, err = editor.RemoveDependency(manifest.TableDependencies, "local-helper")
	require.NoError(t, err)

	_, err = editor.SetDependency(manifest.TableDevDependencies, "luacov", ">=0.15.0")
	require.NoError(t, err)

	_, err = editor.SetDependency(manifest.TableDependencies, "checks", ">=5.0.0")
	require.NoError(t, err)

	mfst := reparse(t, editor.Bytes())
	assert.Equal(t, ">=5.0.0", mfst.Dependencies["checks"].Version)
	assert.NotContains(t, mfst.Dependencies, "local-helper")
	assert.Equal(t, ">=0.15.0", mfst.DevDependencies["luacov"].Version)
	assert.Equal(t, ">=1.0.0", mfst.DevDependencies["luatest"].Version)
	assert.Contains(t, string(editor.Bytes()), "# app.manifest.toml for demo")
}

// TestEditorQuotesWhatItWrites checks a constraint is escaped rather than
// pasted, so a value with a quote in it cannot break the document open.
func TestEditorQuotesWhatItWrites(t *testing.T) {
	t.Parallel()

	editor := edit(t, editHeader)

	_, err := editor.SetDependency(manifest.TableDependencies, "odd", `>=1.0.0"\ `)
	require.NoError(t, err)

	assert.Contains(t, string(editor.Bytes()), `odd = ">=1.0.0\"\\ "`)

	mfst, _, err := manifest.ParseManifest(editor.Bytes())
	require.NoError(t, err)
	assert.Equal(t, `>=1.0.0"\ `, mfst.Dependencies["odd"].Version)
}
