package pack

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
)

// spell-checker:ignore blabla fooo

// transformSlice is a generic mapper function that returns slice with all items
// mapped from the originals with fn function.
func transformSlice[S ~[]E0, E0, E any](src S, fn func(E0) E) []E {
	result := make([]E, len(src))
	for i, v := range src {
		result[i] = fn(v)
	}
	return result
}

// transformMapValues is a generic mapper function that returns map with the same keys
// and values mapped from the originals with fn function.
func transformMapValues[K comparable, V0, V any](src map[K]V0, fn func(V0) V) map[K]V {
	result := make(map[K]V, len(src))
	for k, v := range src {
		result[k] = fn(v)
	}
	return result
}

// There are 3 ways to prepare the bunch of test cases for the concrete function:
// 1. Generate from the common test data (transformMapValues against common test data).
// 2. Transform from another bunch (transformMapValues against another bunch).
// 3. Manual (just initialize bunch with the desired cases).

// ignoreTestData is used to define the very base data set that can be used as a source
// to generate the actual testcases suitable for the concrete test functions.
type ignoreTestData struct {
	pattern    string
	matches    []string
	mismatches []string
}

// The 'pattern' field of any item from this data set refers to name (no path separator).
// The 'matches'/'mismatches' fields must contain only names as well. This constraint allows
// to expand corresponding test cases for the certain function in a more convenient way.
var ignoreTestData_namesOnly = map[string]ignoreTestData{
	"simple_name": {
		pattern: "foo",
		matches: []string{
			"foo",
		},
		mismatches: []string{
			"foo2",
			".foo",
			"blabla_foo",
			"foo_blabla",
			"bla_foo_bla",
		},
	},
	"name_with_space": {
		pattern: "foo with space",
		matches: []string{
			"foo with space",
		},
		mismatches: []string{
			"foo with space2",
			".foo with space",
			"blabla_foo with space",
			"foo with space_blabla",
			"bla_foo with space_bla",
		},
	},
	"name_ends_with_space": {
		pattern: "foo_ends_with_space\\ ",
		matches: []string{
			"foo_ends_with_space ",
		},
		mismatches: []string{
			"foo_ends_with_space",
			"foo_ends_with_space  ",
			".foo_ends_with_space ",
			"blabla_foo_ends_with_space ",
			"foo_ends_with_space blabla",
			"bla_foo_ends_with_space bla",
		},
	},
	"name_with_brackets": {
		pattern: "foo(with_brackets)",
		matches: []string{
			"foo(with_brackets)",
		},
		mismatches: []string{
			"foo(with_brackets)2",
			".foo(with_brackets)",
			"blabla_foo(with_brackets)",
			"foo(with_brackets)_blabla",
			"bla_foo(with_brackets)_bla",
		},
	},
	"name_with_curly_brackets": {
		pattern: "foo{with_curly_brackets}",
		matches: []string{
			"foo{with_curly_brackets}",
		},
		mismatches: []string{
			"foo{with_curly_brackets}2",
			".foo{with_curly_brackets}",
			"blabla_foo{with_curly_brackets}",
			"foo{with_curly_brackets}_blabla",
			"bla_foo{with_curly_brackets}_bla",
		},
	},
	"name_with_plus": {
		pattern: "f+oo",
		matches: []string{
			"f+oo",
		},
		mismatches: []string{
			"f+oo2",
			"ffoo", // spell-checker:disable-line
			".f+oo",
			"blabla_f+oo2",
			"f+oo2_blabla",
			"bla_f+oo2_bla",
		},
	},
	"name_with_escaped_square_brackets": {
		pattern: "foo\\[with_escaped_square_brackets\\]",
		matches: []string{
			"foo[with_escaped_square_brackets]",
		},
		mismatches: []string{
			"foo[with_escaped_square_brackets]2",
			".foo[with_escaped_square_brackets]",
			"blabla_foo[with_escaped_square_brackets]2",
			"foo[with_escaped_square_brackets]2_blabla",
			"bla_foo[with_escaped_square_brackets]2_bla",
		},
	},
	"name_with_escaped_question": {
		pattern: "foo\\?with_escaped_question",
		matches: []string{
			"foo?with_escaped_question",
		},
		mismatches: []string{
			"foo?with_escaped_question2",
			".foo?with_escaped_question",
			"foo2with_escaped_question",
			"blabla_foo?with_escaped_question2",
			"foo?with_escaped_question2_blabla",
			"bla_foo?with_escaped_question2_bla",
		},
	},
	"name_with_escaped_asterisk": {
		pattern: "foo\\*with_escaped_asterisk",
		matches: []string{
			"foo*with_escaped_asterisk",
		},
		mismatches: []string{
			"foo*with_escaped_asterisk2",
			".foo*with_escaped_asterisk",
			"blabla_foo*with_escaped_asterisk2",
			"foo*with_escaped_asterisk2_blabla",
			"bla_foo*with_escaped_asterisk2_bla",
		},
	},
	"name_with_question_prefix": {
		pattern: "?foo",
		matches: []string{
			"2foo",
			"?foo",
			".foo",
			"*foo",
		},
		mismatches: []string{
			"foo",
			"foo2",
			"blabla_2foo",
			"2foo_blabla",
			"bla_2foo_bla",
		},
	},
	"name_with_question_suffix": {
		pattern: "foo?",
		matches: []string{
			"foo2",
			"foo?",
			"foo*",
			"foo ",
		},
		mismatches: []string{
			"foo",
			"blabla_foo2",
			"foo2_blabla",
			"bla_foo2_bla",
		},
	},
	"name_with_question_between": {
		pattern: "f?oo",
		matches: []string{
			"f2oo",
			"fooo",
			"f?oo",
			"f*oo",
		},
		mismatches: []string{
			"foo",
			"blabla_f2oo",
			"f2oo_blabla",
			"bla_f2oo_bla",
		},
	},
	"name_with_asterisk_prefix": {
		pattern: "*foo",
		matches: []string{
			"blabla_foo",
			"foo",
			".foo",
			"*foo",
			"?foo",
		},
		mismatches: []string{
			"foo2",
			"2foo_blabla",
			"bla_2foo_bla",
		},
	},
	"name_with_asterisk_suffix": {
		pattern: "foo*",
		matches: []string{
			"foo_blabla",
			"foo",
			"foo*",
			"foo?",
		},
		mismatches: []string{
			"2foo",
			"blabla_2foo",
			"2foo_blabla",
			"bla_2foo_bla",
		},
	},
	"name_with_asterisk_between": {
		pattern: "f*oo",
		matches: []string{
			"f2oo",
			"foo",
			"f*oo",
			"f?oo",
		},
		mismatches: []string{
			"foo2",
			"blabla_foo2",
			"foo2_blabla",
			"bla_foo2_bla",
		},
	},
	"name_with_range_basic": {
		pattern: "f[n-p]o",
		matches: []string{
			"fno",
			"foo",
			"fpo",
		},
		mismatches: []string{
			"f2o",
			"fmo",
			"fqo",
			"f?o",
			"blabla_foo",
			"foo_blabla",
			"bla_foo_bla",
		},
	},
	"name_with_range_inverted": {
		pattern: "f[^n-p]o",
		matches: []string{
			"f2o",
			"fmo",
			"fqo",
			"f?o",
		},
		mismatches: []string{
			"foo",
			"fno",
			"fpo",
		},
	},
	"name_with_set_basic": {
		pattern: "[fgm]oo",
		matches: []string{
			"foo",
			"goo",
			"moo",
		},
		mismatches: []string{
			"zoo",
			"ooo",
			"?oo",
			"blabla_foo",
			"foo_blabla",
			"bla_foo_bla",
		},
	},
	"name_with_set_inverted": {
		pattern: "[^fgm]oo",
		matches: []string{
			"zoo",
			"ooo",
			"?oo",
		},
		mismatches: []string{
			"foo",
			"goo",
			"moo",
			"blabla_zoo",
			"zoo_blabla",
			"bla_zoo_bla",
		},
	},
}

type ignorePatternCase struct {
	pattern    string
	matches    []string
	mismatches []string
	dirOnly    bool
	isNegate   bool
}

type ignorePatternCases map[string]ignorePatternCase

func checkIgnorePattern(t *testing.T, testCases ignorePatternCases) {
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			p, err := createIgnorePattern(tc.pattern, "")
			assert.Nil(t, err)
			assert.NotNil(t, p.re)
			assert.Equal(t, tc.dirOnly, p.dirOnly)
			assert.Equal(t, tc.isNegate, p.isNegate)
			for _, s := range tc.matches {
				assert.Truef(t, p.re.MatchString(s), "%q doesn't match %s", s, p.re.String())
			}
			for _, s := range tc.mismatches {
				assert.False(t, p.re.MatchString(s), "%q matches %s", s, p.re.String())
			}
		})
	}
}

// Prepare a kind of base test set for createIgnorePattern function.
// It is generated from the common test data and thus consists of ordinary patterns only.
// Negate and directory patterns are tested in separate functions. Cases for them are generated
// by adjusting cases of this base set (note that these adjustments don't affect
// matches/mismatches because matching part of every pattern stays the same).
var ignorePatternBaseCases = transformMapValues(ignoreTestData_namesOnly,
	func(td ignoreTestData) ignorePatternCase {
		return ignorePatternCase{
			pattern: td.pattern,
			// Expand with some meaningful paths (td.matches itself has no path-item).
			matches: append(td.matches,
				"in_subdir/"+td.matches[0],
				"in/deep/nested/subdir/"+td.matches[0],
			),
			mismatches: td.mismatches,
			dirOnly:    false,
			isNegate:   false,
		}
	})

func Test_createIgnorePattern_basic(t *testing.T) {
	checkIgnorePattern(t, ignorePatternBaseCases)
}

func Test_createIgnorePattern_negate(t *testing.T) {
	turnToNegate := func(tc ignorePatternCase) ignorePatternCase {
		tc.pattern = "!" + tc.pattern
		tc.isNegate = true
		return tc
	}
	testCases := transformMapValues(ignorePatternBaseCases, turnToNegate)
	checkIgnorePattern(t, testCases)
}

func Test_createIgnorePattern_dirOnly(t *testing.T) {
	turnToDirOnly := func(tc ignorePatternCase) ignorePatternCase {
		tc.pattern = tc.pattern + "/"
		tc.dirOnly = true
		return tc
	}
	testCases := transformMapValues(ignorePatternBaseCases, turnToDirOnly)
	checkIgnorePattern(t, testCases)
}

func Test_createIgnorePattern_trailingSpace(t *testing.T) {
	addTrailingSpace := func(tc ignorePatternCase) ignorePatternCase {
		tc.pattern = tc.pattern + strings.Repeat(" ", 1+len(tc.pattern)%3)
		return tc
	}
	testCases := transformMapValues(ignorePatternBaseCases, addTrailingSpace)
	checkIgnorePattern(t, testCases)
}

// NOTE: For a new test that is not based on the base set the below snippet can be used.
// func Test_createIgnorePattern_someNewTest(t *testing.T) {
// 	testCases := ignorePatternCases{
// 		"case_name1": {...},
// 		"case_name2": {...},
// 	}
// 	checkIgnorePattern(t, testCases)
// }

type ignoreFilterCase struct {
	// Ignore patterns.
	patterns []string
	// Files that are expected to be ignored/copied during copy.
	// Every item here denotes file (not directory).
	ignored []string
	copied  []string
	// Nodes that represents nested .packignore files (if any).
	children map[string]ignoreFilterCase
}

type ignoreFilterCases map[string]ignoreFilterCase

const ignoreFilterRootDir = "src"

func (tc ignoreFilterCase) createFS(t *testing.T) fs.FS {
	t.Helper()

	files := slices.Concat(tc.filesToCopy(), tc.filesToIgnore())

	// Check that no entry ends with '/' and all files are able to coexist within a single FS
	// (the same path should not refer to a file and a directory simultaneously).
	slices.Sort(files)
	for i, f := range files {
		assert.Falsef(t, strings.HasSuffix(f, "/"), "Invalid test case: %q ends with '/'", f)
		if i > 0 {
			assert.Falsef(t, strings.HasPrefix(f, files[i-1]+"/"),
				"Invalid test case: %q and %q are not able to coexist within single FS",
				f,
				files[i-1],
			)
		}
	}

	fsys := fstest.MapFS{}
	tc.createFiles(fsys, ignoreFilterRootDir)
	return fsys
}

func (tc ignoreFilterCase) filesToCopy() []string {
	files := tc.copied
	for key, child := range tc.children {
		childFiles := transformSlice(child.filesToCopy(), func(file string) string {
			return filepath.Join(key, file)
		})
		files = append(files, childFiles...)
	}
	return files
}

func (tc ignoreFilterCase) filesToIgnore() []string {
	files := slices.Concat([]string{ignoreFile}, tc.ignored)
	for key, child := range tc.children {
		childFiles := transformSlice(child.filesToIgnore(), func(file string) string {
			return filepath.Join(key, file)
		})
		files = append(files, childFiles...)
	}
	return files
}

func (tc ignoreFilterCase) createFiles(fsys fstest.MapFS, prefix string) {
	if tc.patterns != nil {
		fsys[filepath.Join(prefix, ignoreFile)] = &fstest.MapFile{
			Data: []byte(strings.Join(tc.patterns, "\n")),
			Mode: fs.FileMode(0o644),
		}
	}
	for _, name := range slices.Concat(tc.copied, tc.ignored) {
		fsys[filepath.Join(prefix, name)] = &fstest.MapFile{
			Mode: fs.FileMode(0o644),
		}
	}

	// Create files for nested .packignore recursively.
	for key, child := range tc.children {
		child.createFiles(fsys, filepath.Join(prefix, key))
	}
}

func checkIgnoreFilter(t *testing.T, testCases ignoreFilterCases) {
	t.Helper()

	baseDst := t.TempDir()

	// Do test
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			fsys := tc.createFS(t)

			filter, err := createIgnoreFilter(fsys, ignoreFilterRootDir, ignoreFile)
			assert.Nil(t, err)
			assert.NotNil(t, filter)

			dst := filepath.Join(baseDst, name)
			err = os.MkdirAll(dst, 0o755)
			assert.Nil(t, err)

			err = copy.Copy(ignoreFilterRootDir, dst, copy.Options{
				FS: fsys,
				Skip: func(srcInfo os.FileInfo, src, dest string) (bool, error) {
					return filter.shouldSkip(srcInfo, src), nil
				},
				PermissionControl: copy.AddPermission(0o755),
			})
			assert.Nil(t, err)
			for _, name := range tc.filesToIgnore() {
				assert.NoFileExists(t, path.Join(dst, name))
			}
			for _, name := range tc.filesToCopy() {
				assert.FileExists(t, path.Join(dst, name))
			}
		})
	}
}

// Prepare a kind of base test set for ignoreFilter function.
// It is generated from the common test data and contains only cases with single ordinary pattern.
var ignoreFilterBaseCases = transformMapValues(ignoreTestData_namesOnly,
	func(td ignoreTestData) ignoreFilterCase {
		// Make sure pattern contains no path separator.
		if strings.Contains(td.pattern, "/") {
			panic("unexpected path separator in pattern")
		}
		return ignoreFilterCase{
			patterns: []string{
				td.pattern,
			},
			// Expand with some meaningful paths (td.matches itself has no path-item).
			ignored: append(td.matches,
				"in_subdir/"+td.matches[0],
				"in/deep/nested/subdir/"+td.matches[0],
				"as_subdir/"+td.matches[0]+"/bar",
				"as_subdir/"+td.matches[0]+"/with_nested_subdir/bar",
				"as/deep/nested/subdir/"+td.matches[0]+"/bar",
				"as/deep/nested/subdir/"+td.matches[0]+"/with_nested_subdir/bar",
			),
			// Expand with some meaningful paths (td.mismatches itself has no path-item).
			copied: append(td.mismatches,
				"in_subdir/"+td.mismatches[0],
				"in/deep/nested/subdir/"+td.mismatches[0],
			),
		}
	})

func Test_createIgnoreFilter_noIgnoreFile(t *testing.T) {
	f, err := createIgnoreFilter(fstest.MapFS{}, "", ignoreFile)
	assert.Nil(t, err)
	assert.NotNil(t, f)
}

func Test_createIgnoreFilter_singleBasic(t *testing.T) {
	checkIgnoreFilter(t, ignoreFilterBaseCases)
}

func Test_createIgnoreFilter_singleNegate(t *testing.T) {
	// Single negate pattern has no effect (i.e. all files are copied).
	toSingleNegate := func(tc ignoreFilterCase) ignoreFilterCase {
		return ignoreFilterCase{
			patterns: []string{
				"!" + tc.patterns[0],
			},
			ignored: nil,
			copied:  slices.Concat(tc.copied, tc.ignored),
		}
	}
	testCases := transformMapValues(ignoreFilterBaseCases, toSingleNegate)
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_selfNegate(t *testing.T) {
	// An ignore pattern followed by the same but negated (thus it just reinclude all).
	toSelfNegate := func(tc ignoreFilterCase) ignoreFilterCase {
		return ignoreFilterCase{
			patterns: []string{
				tc.patterns[0],
				"!" + tc.patterns[0],
			},
			ignored: nil,
			copied:  slices.Concat(tc.copied, tc.ignored),
		}
	}
	testCases := transformMapValues(ignoreFilterBaseCases, toSelfNegate)
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_negateWrongOrder(t *testing.T) {
	// A negate pattern in a wrong position doesn't affect the original result.
	toWrongOrderNegate := func(tc ignoreFilterCase) ignoreFilterCase {
		return ignoreFilterCase{
			patterns: []string{
				"!" + tc.patterns[0],
				tc.patterns[0],
			},
			ignored: tc.ignored,
			copied:  tc.copied,
		}
	}
	testCases := transformMapValues(ignoreFilterBaseCases, toWrongOrderNegate)
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_singleDir(t *testing.T) {
	// Generate test set from the common test data rather than from the base set
	// because result for this set differ significantly from the original.
	testCases := transformMapValues(ignoreTestData_namesOnly,
		func(td ignoreTestData) ignoreFilterCase {
			return ignoreFilterCase{
				patterns: []string{
					td.pattern + "/",
				},
				// td.matches (as well as td.mismatches) are represented as files so they don't
				// match directory pattern as-is and should be appended as below to become
				// directories that do match.
				ignored: []string{
					td.matches[0] + "/as_dir",
					td.matches[0] + "/as_dir_with_nested_subdir/bar",
					"as_subdir/" + td.matches[0] + "/bar",
					"as_subdir/" + td.matches[0] + "/with_nested_subdir/bar",
					"as/deep/nested/subdir/" + td.matches[0] + "/bar",
					"as/deep/nested/subdir/" + td.matches[0] + "/with_nested_subdir/bar",
				},
				// Note that matches[0] is excluded because it can't coexist with ignored[0] and
				// ignored[1] within a single FS.
				copied: slices.Concat(
					td.mismatches,
					td.matches[1:],
					[]string{
						"in_subdir/" + td.matches[0],
						"in/deep/nested/subdir/" + td.matches[0],
					},
				),
			}
		})
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_multiNames(t *testing.T) {
	testCases := ignoreFilterCases{
		"any": {
			patterns: []string{
				"name1",
				"name2",
			},
			ignored: []string{
				"name1",
				"in_subdir/name1",
				"in/deep/nested/subdir/name1",
				"as_subdir/name1/foo",
				"as/deep/nested/subdir/name1/bar",
				"name2",
				"in_subdir/name2",
				"in/deep/nested/subdir/name2",
				"as_subdir/name2/foo",
				"as/deep/nested/subdir/name2/bar",
			},
			copied: []string{
				"name3",
				"name4",
			},
		},
		"dironly": {
			patterns: []string{
				"name1/",
				"name2/",
			},
			ignored: []string{
				"as_subdir/name1/foo",
				"as/deep/nested/subdir/name1/bar",
				"as_subdir/name2/foo",
				"as/deep/nested/subdir/name2/bar",
			},
			copied: []string{
				"name1",
				"in_subdir/name1",
				"in/deep/nested/subdir/name1",
				"name2",
				"in_subdir/name2",
				"in/deep/nested/subdir/name2",
				"name3",
				"name4",
			},
		},
		"mixed": {
			patterns: []string{
				"name1",
				"name2/",
			},
			ignored: []string{
				"name1",
				"in_subdir/name1",
				"in/deep/nested/subdir/name1",
				"as_subdir/name1/foo",
				"as/deep/nested/subdir/name1/bar",
				"as_subdir/name2/bar",
				"as/deep/nested/subdir/name2/bar",
			},
			copied: []string{
				"name2",
				"in_subdir/name2",
				"in/deep/nested/subdir/name2",
				"name3",
				"name4",
			},
		},
	}
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_fixedDepth(t *testing.T) {
	testCases := ignoreFilterCases{
		"name_at_depth1": {
			patterns: []string{
				"*/foo",
			},
			ignored: []string{
				"in_subdir/foo",
				"in_another_subdir/foo",
				"as_subdir/foo/bar",
				"as_another_subdir/foo/bar",
			},
			copied: []string{
				"foo",
				"in/subdir/of/another/depth/foo",
				"as/subdir/of/another/depth/foo/bar",
				"foo2",
				"similar_in_subdir/foo2",
				"similar_as_subdir/foo2/bar",
			},
		},
		"name_at_depth2": {
			patterns: []string{
				"*/*/foo",
			},
			ignored: []string{
				"in_subdir/of_depth2/foo",
				"in_another_subdir/of_depth2/foo",
				"as_subdir/of_depth2/foo/bar",
				"as_another_subdir/of_depth2/foo/bar",
			},
			copied: []string{
				"foo",
				"in/subdir/of/another/depth/foo",
				"as/subdir/of/another/depth/foo/bar",
				"foo2",
				"similar_in_subdir/of_depth2/foo2",
				"similar_as_subdir/of_depth2/foo2/bar",
			},
		},
		"under_name_depth1": {
			patterns: []string{
				"foo/*",
			},
			ignored: []string{
				"foo/bar",
				"foo/blabla",
				"foo/with_subdir/bar",
				"foo/with_subdir/blabla",
			},
			copied: []string{
				"as_subdir/foo/bar",
				"as/subdir/of/another/depth/foo/bar",
				"foo2/bar",
				"foo2/blabla",
			},
		},
		"under_name_depth2": {
			patterns: []string{
				"foo/*/*",
			},
			ignored: []string{
				"foo/subdir/bar",
				"foo/subdir/blabla",
				"foo/another_subdir/bar",
			},
			copied: []string{
				"as_subdir/foo/subdir/bar",
				"as/subdir/of/another/depth/foo/subdir/bar",
				"foo/bar",
				"foo/blabla",
				"foo2/subdir/bar",
			},
		},
	}
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_reinclude(t *testing.T) {
	testCases := ignoreFilterCases{
		"by_name": {
			patterns: []string{
				"*name?",
				"!renamed",
			},
			ignored: []string{
				"name1",
				"in_subdir/filename2",
				"in/deep/nested/subdir/rename3",
				"as_subdir/dirname4/bar",
				"as_subdir/dirname4/renamed",
				"as/deep/nested/subdir/newname5/bar",
				"as/deep/nested/subdir/newname5/renamed",
			},
			copied: []string{
				"renamed",
				"in_subdir/renamed",
				"as_subdir/renamed/bar",
				"name13",
				"rename14",
			},
		},
		"by_names": {
			patterns: []string{
				"*name?",
				"!renamed",
				"!unnamed",
			},
			ignored: []string{
				"name1",
				"newname2",
				"oldname3",
				"in_subdir/filename2",
				"in/deep/nested/subdir/rename3",
				"as_subdir/dirname4/bar",
				"as_subdir/dirname4/renamed",
				"as/deep/nested/subdir/newname5/bar",
				"as/deep/nested/subdir/newname5/renamed",
			},
			copied: []string{
				"renamed",
				"in_subdir/renamed",
				"as_subdir/renamed/bar",
				"unnamed",
				"in_subdir/unnamed",
				"as_subdir/unnamed/bar",
				"name13",
			},
		},
		"by_pattern": {
			patterns: []string{
				"*name?",
				"!*named",
			},
			ignored: []string{
				"name1",
				"newname2",
				"oldname3",
				"in_subdir/filename2",
				"in/deep/nested/subdir/rename3",
				"as_subdir/dirname4/bar",
				"as_subdir/dirname4/renamed",
				"as/deep/nested/subdir/newname5/bar",
				"as/deep/nested/subdir/newname5/renamed",
				"as/deep/nested/subdir/newname5/unnamed",
			},
			copied: []string{
				"renamed",
				"in_subdir/renamed",
				"as_subdir/renamed/bar",
				"unnamed",
				"in_subdir/unnamed",
				"as_subdir/unnamed/bar",
				"name13",
			},
		},
	}
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_doubleAsterisk(t *testing.T) {
	testCases := ignoreFilterCases{
		"leading": {
			patterns: []string{
				"**/foo",
			},
			ignored: []string{
				"foo",
				"in_subdir/foo",
				"in/deep/nested/subdir/foo",
			},
			copied: []string{
				"foo2",
				"similar_in_subdir/foo2",
				"similar/in/deep/nested/subdir/foo2",
				"subdir/foo2/bar",
			},
		},
		"trailing": {
			patterns: []string{
				"foo/**",
			},
			ignored: []string{
				"foo/bar",
				"foo/with_subdir/bar",
				"foo/with/deep/nested/subdir/bar",
			},
			copied: []string{
				"foo_blabla",
				"file_in_subdir/foo",
				"file/in/deep/nested/subdir/foo",
				"similar_subdir/foo2/bar",
			},
		},
		"inner": {
			patterns: []string{
				"foo/**/bar",
			},
			ignored: []string{
				"foo/bar",
				"foo/subdir/bar",
				"foo/deep/nested/subdir/bar",
			},
			copied: []string{
				"foo/bar2",
				"foo/with_subdir/bar2",
				"foo/with/deep/nested/subdir/bar2",
				"foo2",
				"similar_in_subdir/foo2",
				"similar/in/deep/nested/subdir/foo2",
				"subdir/foo2/bar",
			},
		},
	}
	checkIgnoreFilter(t, testCases)
}

func Test_createIgnoreFilter_nested(t *testing.T) {
	testCases := map[string]ignoreFilterCase{
		"single_child": {
			patterns: []string{"root"},
			ignored:  []string{"root"},
			copied:   []string{"child", "another_mismatch"},
			children: map[string]ignoreFilterCase{
				"dir_child": {
					patterns: []string{"child"},
					ignored:  []string{"child", "root"},
					copied:   []string{"another_mismatch"},
				},
			},
		},
		"nested_dir_child": {
			patterns: []string{"root"},
			ignored:  []string{"root"},
			copied:   []string{"child", "another_mismatch"},
			children: map[string]ignoreFilterCase{
				"nested/dir_child": {
					patterns: []string{"child"},
					ignored:  []string{"child", "root"},
					copied:   []string{"another_mismatch"},
				},
			},
		},
		"siblings": {
			patterns: []string{"root"},
			ignored:  []string{"root"},
			copied:   []string{"child1", "child2", "another_mismatch"},
			children: map[string]ignoreFilterCase{
				"dir_child1": {
					patterns: []string{"child1"},
					ignored:  []string{"child1", "root"},
					copied:   []string{"child2", "another_mismatch"},
				},
				"dir_child2": {
					patterns: []string{"child2"},
					ignored:  []string{"child2", "root"},
					copied:   []string{"child1", "another_mismatch"},
				},
			},
		},
		"grandchild": {
			patterns: []string{"root"},
			ignored:  []string{"root"},
			copied:   []string{"child", "grandchild", "another_mismatch"},
			children: map[string]ignoreFilterCase{
				"dir_child": {
					patterns: []string{"child"},
					ignored:  []string{"child", "root"},
					copied:   []string{"grandchild", "another_mismatch"},
					children: map[string]ignoreFilterCase{
						"dir_grandchild": {
							patterns: []string{"grandchild"},
							ignored:  []string{"grandchild", "child", "root"},
							copied:   []string{"another_mismatch"},
						},
					},
				},
			},
		},
		"missing_root": {
			patterns: nil,
			ignored:  nil,
			copied:   []string{"root", "child", "another_mismatch"},
			children: map[string]ignoreFilterCase{
				"dir_child": {
					patterns: []string{"child"},
					ignored:  []string{"child"},
					copied:   []string{"root", "another_mismatch"},
				},
			},
		},
		"reinclude": {
			patterns: []string{"root"},
			ignored:  []string{"root"},
			copied:   []string{"child1", "child2", "another_mismatch"},
			children: map[string]ignoreFilterCase{
				"dir_child1": {
					patterns: []string{"child1", "!root"},
					ignored:  []string{"child1"},
					copied:   []string{"root", "child2", "another_mismatch"},
				},
				"dir_child2": {
					patterns: []string{"child2"},
					ignored:  []string{"child2", "root"},
					copied:   []string{"child1", "another_mismatch"},
				},
			},
		},
	}
	checkIgnoreFilter(t, testCases)
}
