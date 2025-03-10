package pack

import (
	"errors"
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

// ignorePatternCase is used to define the very basic data set
// which then used as a source to generate the actual testcases
// suitable for the corresponding test functions.
type ignorePatternCase struct {
	pattern    string
	matches    []string
	mismatches []string
	dirOnly    bool
	isNegate   bool
}

type ignorePatternCases = map[string]ignorePatternCase

type transformPattern func(*ignorePatternCase)

// preparePatterns return copy of the given data set with the
// transformation applied to each item.
func preparePatterns(src ignorePatternCases, fn transformPattern) ignorePatternCases {
	result := make(ignorePatternCases, len(src))
	for name, tc := range src {
		fn(&tc)
		result[name] = tc
	}
	return result
}

// The 'pattern' field of any item from this data set refers to name (no path separator).
// The 'matches'/'mismatches' fields must contain only names as well. This constraint allows
// to expand corresponding test cases for the certain function in a more convenient way.
var ignoreTestCaseNames = preparePatterns(ignorePatternCases{
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
	"with_space": {
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
	"ends_with_space": {
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
	"with_brackets": {
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
	"with_curly_brackets": {
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
	"with_plus": {
		pattern: "f+oo",
		matches: []string{
			"f+oo",
		},
		mismatches: []string{
			"f+oo2",
			"ffoo",
			".f+oo",
			"blabla_f+oo2",
			"f+oo2_blabla",
			"bla_f+oo2_bla",
		},
	},
	"with_escaped_square_brackets": {
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
	"with_escaped_question": {
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
	"with_escaped_asterisk": {
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
	"with_question_prefix": {
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
	"with_question_suffix": {
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
	"with_question_between": {
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
	"with_asterisk_prefix": {
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
	"with_asterisk_suffix": {
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
	"with_asterisk_between": {
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
	"with_range_basic": {
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
	"with_range_inverted": {
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
	"with_set_basic": {
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
	"with_set_inverted": {
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
}, func(tc *ignorePatternCase) {
	// Extend each case with some meaningful paths.
	tc.matches = append(tc.matches,
		"in_subdir/"+tc.matches[0],
		"in/deep/nested/subdir/"+tc.matches[0],
	)
})

var ignoreTestCaseWildcard = ignorePatternCases{
	"at_depth1": {
		pattern: "*/foo",
		matches: []string{
			"in_subdir/foo",
			"in_another_subdir/foo",
		},
		mismatches: []string{
			"foo",
			"in/subdir/of/another/depth/foo",
			"foo2",
			"similar_in_subdir/foo2",
		},
	},
	"at_depth2": {
		pattern: "*/*/foo",
		matches: []string{
			"in_subdir/of_depth2/foo",
			"in_another_subdir/of_depth2/foo",
		},
		mismatches: []string{
			"foo",
			"in/subdir/of/another/depth/foo",
			"foo2",
			"similar_in_subdir/of_depth2/foo2",
		},
	},
	"under_name_depth1": {
		pattern: "foo/*",
		matches: []string{
			"foo/bar",
			"foo/blabla",
		},
		mismatches: []string{
			"foo",
			"foo2/bar",
			"foo2/blabla",
		},
	},
	"under_name_depth2": {
		pattern: "foo/*/*",
		matches: []string{
			"foo/subdir/bar",
			"foo/subdir/blabla",
			"foo/another_subdir/bar",
		},
		mismatches: []string{
			"foo",
			"foo/bar",
			"foo/blabla",
			"foo2/subdir/bar",
		},
	},
	"with_double_asterisk_leading": {
		pattern: "**/foo",
		matches: []string{
			"foo",
			"in_subdir/foo",
			"in/deep/nested/subdir/foo",
		},
		mismatches: []string{
			"foo2",
			"similar_in_subdir/foo2",
			"similar/in/deep/nested/subdir/foo2",
			"subdir/foo2/bar",
		},
	},
	"with_double_asterisk_trailing": {
		pattern: "foo/**",
		matches: []string{
			"foo/bar",
			"foo/with_subdir/bar",
			"foo/with/deep/nested/subdir/bar",
		},
		mismatches: []string{
			"foo",
			"foo_blabla",
			"file_in_subdir/foo",
			"file/in/deep/nested/subdir/foo",
			"similar_subdir/foo2/bar",
		},
	},
	"with_inner_double_asterisk": {
		pattern: "foo/**/bar",
		matches: []string{
			"foo/bar",
			"foo/subdir/bar",
			"foo/deep/nested/subdir/bar",
		},
		mismatches: []string{
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

func mergeTestCases(cases ...ignorePatternCases) ignorePatternCases {
	result := make(ignorePatternCases)
	for _, tc := range cases {
		for name, data := range tc {
			if _, ok := result[name]; ok {
				panic("duplicate test case name: " + name)
			}
			result[name] = data
		}
	}
	return result
}

var ignoreBaseTestCasesPattern = mergeTestCases(ignoreTestCaseNames, ignoreTestCaseWildcard)

// NOTE: For a new test that is not based on basic set the below snippet can be used.
// func Test_createIgnorePattern_someNewTest(t *testing.T) {
//     tc := ignorePatternCase{
//         "test_name": {...},
//     }
//     checkIgnorePattern(t, tc)
// }

func checkIgnorePattern(t *testing.T, cases ignorePatternCases) {
	t.Helper()

	for name, tc := range cases {
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

func Test_createIgnorePattern_basic(t *testing.T) {
	checkIgnorePattern(t, ignoreBaseTestCasesPattern)
}

func Test_createIgnorePattern_negate(t *testing.T) {
	invertPattern := func(td *ignorePatternCase) {
		td.pattern = "!" + td.pattern
		td.isNegate = true
	}

	tc := preparePatterns(ignoreBaseTestCasesPattern, invertPattern)
	checkIgnorePattern(t, tc)
}

func Test_createIgnorePattern_dirOnly(t *testing.T) {
	addDirSlash := func(td *ignorePatternCase) {
		td.pattern += "/"
		td.dirOnly = true
	}

	tc := preparePatterns(ignoreBaseTestCasesPattern, addDirSlash)
	checkIgnorePattern(t, tc)
}

func Test_createIgnorePattern_trailingSpace(t *testing.T) {
	addTrailingSpace := func(td *ignorePatternCase) {
		td.pattern += strings.Repeat(" ", 1+len(td.pattern)%3)
	}

	tc := preparePatterns(ignoreBaseTestCasesPattern, addTrailingSpace)
	checkIgnorePattern(t, tc)
}

type ignoreFilterCase struct {
	// Test name.
	name string
	// Ignore patterns.
	patterns []string
	// Files that are expected to be ignored/copied during copy.
	// Make sure all of them are able to exist within a single FS (the same name should not
	// refer to a file and a directory).
	ignored []string
	copied  []string
}

type makeFilterFromPattern func(ignorePatternCase) ignoreFilterCase

func prepareFiltersFromPattern(
	src ignorePatternCases,
	fn makeFilterFromPattern,
) []ignoreFilterCase {
	result := make([]ignoreFilterCase, 0, len(src))
	for name, tc := range src {
		f := fn(tc)
		f.name = name
		result = append(result, f)
	}
	return result
}

type transformFilter func(*ignoreFilterCase)

func prepareFilters(src []ignoreFilterCase, fn transformFilter) []ignoreFilterCase {
	result := make([]ignoreFilterCase, len(src))
	for i, tc := range src {
		fn(&tc)
		result[i] = tc
	}
	return result
}

// createFS function to create mock FS for the testcase.
func createFS(tc ignoreFilterCase) fs.FS {
	f := fstest.MapFS{}
	if tc.patterns != nil {
		f[ignoreFile] = &fstest.MapFile{
			Data: []byte(strings.Join(tc.patterns, "\n")),
			Mode: fs.FileMode(0644),
		}
	}
	for _, name := range slices.Concat(tc.copied, tc.ignored) {
		f[name] = &fstest.MapFile{
			Mode: fs.FileMode(0644),
		}
	}
	return f
}

func checkIgnoreFilter(t *testing.T, cases []ignoreFilterCase) {
	t.Helper()
	dir := t.TempDir()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := createFS(tc)

			filter, err := ignoreFilter(fsys, ignoreFile)
			assert.Nil(t, err)
			assert.NotNil(t, filter)

			dst := filepath.Join(dir, tc.name)
			err = os.MkdirAll(dst, 0755)
			if err != nil {
				assert.Nil(t, err)
			}

			err = copy.Copy(".", dst, copy.Options{
				FS: fsys,
				Skip: func(srcinfo os.FileInfo, src, dest string) (bool, error) {
					return filter(srcinfo, src), nil
				},
				PermissionControl: copy.AddPermission(0755),
			})
			assert.Nil(t, err)
			for _, name := range tc.ignored {
				assert.NoFileExists(t, path.Join(dst, name))
			}
			for _, name := range tc.copied {
				assert.FileExists(t, path.Join(dst, name))
			}
		})
	}
}

// Prepare basic test set for ignoreFilter function.
var ignoreBaseTestCasesFilter = prepareFiltersFromPattern(ignoreTestCaseNames,
	func(td ignorePatternCase) ignoreFilterCase {
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

func Test_ignoreFilter_noIgnoreFile(t *testing.T) {
	f, err := ignoreFilter(fstest.MapFS{}, ignoreFile)
	assert.NotNil(t, err)
	assert.True(t, errors.Is(err, fs.ErrNotExist))
	assert.Nil(t, f)
}

func Test_ignoreFilter_singleBasic(t *testing.T) {
	checkIgnoreFilter(t, ignoreBaseTestCasesFilter)
}

func Test_ignoreFilter_singleNegate(t *testing.T) {
	// Single negate pattern has no effect.
	invertPattern := func(c *ignoreFilterCase) {
		c.patterns = []string{"!" + c.patterns[0]}
		c.copied = slices.Concat(c.copied, c.ignored)
		c.ignored = nil
	}

	tc := prepareFilters(ignoreBaseTestCasesFilter, invertPattern)
	checkIgnoreFilter(t, tc)
}

func Test_ignoreFilter_selfNegate(t *testing.T) {
	// An ignore pattern followed by the same but negated (thus it just reinclude all).
	selfInvert := func(c *ignoreFilterCase) {
		c.patterns = []string{
			c.patterns[0],
			"!" + c.patterns[0],
		}
		c.copied = slices.Concat(c.copied, c.ignored)
		c.ignored = nil
	}

	tc := prepareFilters(ignoreBaseTestCasesFilter, selfInvert)
	checkIgnoreFilter(t, tc)
}

func Test_ignoreFilter_negateWrongOrder(t *testing.T) {
	// An ignore pattern followed by the same but negated (thus it just reinclude all).
	selfInvert := func(c *ignoreFilterCase) {
		c.patterns = []string{
			"!" + c.patterns[0],
			c.patterns[0],
		}
	}

	tc := prepareFilters(ignoreBaseTestCasesFilter, selfInvert)
	checkIgnoreFilter(t, tc)
}

func Test_ignoreFilter_singleDirOnly(t *testing.T) {
	// Generate test set from the common test data rather than from the ignoreFilter basic set
	// because expectations in this case differ significantly.
	tc := prepareFiltersFromPattern(ignoreTestCaseNames,
		func(td ignorePatternCase) ignoreFilterCase {
			return ignoreFilterCase{
				patterns: []string{
					td.pattern + "/",
				},
				// Expand with some meaningful paths (td.matches itself has no path-item).
				ignored: []string{
					td.matches[0] + "/as_dir",
					td.matches[0] + "/as_dir_with_nested_subdir/bar",
					"as_subdir/" + td.matches[0] + "/bar",
					"as_subdir/" + td.matches[0] + "/with_nested_subdir/bar",
					"as/deep/nested/subdir/" + td.matches[0] + "/bar",
					"as/deep/nested/subdir/" + td.matches[0] + "/with_nested_subdir/bar",
				},
				// Expand with some meaningful paths (td.mismatches itself has no path-item).
				copied: slices.Concat(
					td.matches,
					td.mismatches,
					[]string{
						"in_subdir/" + td.mismatches[0],
						"in/deep/nested/subdir/" + td.mismatches[0],
						"in_subdir/" + td.matches[0],
						"in/deep/nested/subdir/" + td.matches[0],
					},
				),
			}
		})

	checkIgnoreFilter(t, tc)
}

func Test_ignoreFilter_multiNames(t *testing.T) {
	tc := []ignoreFilterCase{
		{
			name: "any",
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
		{
			name: "dironly",
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
		{
			name: "mixed",
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
	checkIgnoreFilter(t, tc)
}

func Test_ignoreFilter_fixedDepth(t *testing.T) {
	tc := []ignoreFilterCase{
		{
			name: "name_at_depth1",
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
		{
			name: "name_at_depth2",
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
		{
			name: "under_name_depth1",
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
				"foo",
				"as_subdir/foo/bar",
				"as/subdir/of/another/depth/foo/bar",
				"foo2/bar",
				"foo2/blabla",
			},
		},
		{
			name: "under_name_depth2",
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
	checkIgnoreFilter(t, tc)
}

func Test_ignoreFilter_reinclude(t *testing.T) {
	tc := []ignoreFilterCase{
		{
			name: "by_name",
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
		{
			name: "by_names",
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
		{
			name: "by_pattern",
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
	checkIgnoreFilter(t, tc)
}
