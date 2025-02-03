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

// mapSlice is a generic slice mapper function.
func mapSlice[T, V any](ts []T, fn func(T) V) []V {
	result := make([]V, len(ts))
	for i, t := range ts {
		result[i] = fn(t)
	}
	return result
}

// ignoreTestData is used to define the very basic data set
// which then used as a source to generate the actual testcases
// suitable for the corresponding test functions.
type ignoreTestData struct {
	name       string
	pattern    string
	matches    []string
	mismatches []string
}

// The 'pattern' field of any item from this data set refers to name (no path separator).
// The 'matches'/'mismatches' fields must contain only names as well. This constraint allows
// to expand corresponding test cases for the certain function in a more convenient way.
var ignoreTestData_names = []ignoreTestData{
	{
		name:    "simple_name",
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
	{
		name:    "name_with_space",
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
	{
		name:    "name_ends_with_space",
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
	{
		name:    "name_with_brackets",
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
	{
		name:    "name_with_curly_brackets",
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
	{
		name:    "name_with_plus",
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
	{
		name:    "name_with_escaped_square_brackets",
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
	{
		name:    "name_with_escaped_question",
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
	{
		name:    "name_with_escaped_asterisk",
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
	{
		name:    "name_with_question_prefix",
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
	{
		name:    "name_with_question_suffix",
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
	{
		name:    "name_with_question_between",
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
	{
		name:    "name_with_asterisk_prefix",
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
	{
		name:    "name_with_asterisk_suffix",
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
	{
		name:    "name_with_asterisk_between",
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
	{
		name:    "name_with_range_basic",
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
	{
		name:    "name_with_range_inverted",
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
	{
		name:    "name_with_set_basic",
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
	{
		name:    "name_with_set_inverted",
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

var ignoreTestData_paths = []ignoreTestData{
	{
		name:    "name_at_depth1",
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
	{
		name:    "name_at_depth2",
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
	{
		name:    "under_name_depth1",
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
	{
		name:    "under_name_depth2",
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
	{
		name:    "name_with_double_asterisk_leading",
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
	{
		name:    "name_with_double_asterisk_trailing",
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
	{
		name:    "name_with_inner_double_asterisk",
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

type testCase_ignorePattern struct {
	name               string
	pattern            string
	expectedMatches    []string
	expectedMismatches []string
	expectedDirOnly    bool
	expectedIsNegate   bool
}

func runTestSet_ignorePattern(t *testing.T, testCases []testCase_ignorePattern) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := createIgnorePattern(tc.pattern, "")
			assert.Nil(t, err)
			assert.NotNil(t, p.re)
			assert.Equal(t, tc.expectedDirOnly, p.dirOnly)
			assert.Equal(t, tc.expectedIsNegate, p.isNegate)
			for _, s := range tc.expectedMatches {
				assert.Truef(t, p.re.MatchString(s), "%q doesn't match %s", s, p.re.String())
			}
			for _, s := range tc.expectedMismatches {
				assert.False(t, p.re.MatchString(s), "%q matches %s", s, p.re.String())
			}
		})
	}
}

// Prepare basic test set for createIgnorePattern function
var testCases_ignorePatternBasic = slices.Concat(
	mapSlice(ignoreTestData_names, func(td ignoreTestData) testCase_ignorePattern {
		return testCase_ignorePattern{
			name:    td.name,
			pattern: td.pattern,
			// Expand with some meaningful paths (td.matches itself has no path-item).
			expectedMatches: append(td.matches,
				"in_subdir/"+td.matches[0],
				"in/deep/nested/subdir/"+td.matches[0],
			),
			expectedMismatches: td.mismatches,
			expectedDirOnly:    false,
			expectedIsNegate:   false,
		}
	}),
	mapSlice(ignoreTestData_paths, func(td ignoreTestData) testCase_ignorePattern {
		return testCase_ignorePattern{
			name:               td.name,
			pattern:            td.pattern,
			expectedMatches:    td.matches,
			expectedMismatches: td.mismatches,
			expectedDirOnly:    false,
			expectedIsNegate:   false,
		}
	}),
)

func Test_createIgnorePattern_basic(t *testing.T) {
	runTestSet_ignorePattern(t, testCases_ignorePatternBasic)
}

func Test_createIgnorePattern_negate(t *testing.T) {
	testCases := mapSlice(testCases_ignorePatternBasic,
		func(tc testCase_ignorePattern) testCase_ignorePattern {
			return testCase_ignorePattern{
				name:               tc.name,
				pattern:            "!" + tc.pattern,
				expectedMatches:    tc.expectedMatches,
				expectedMismatches: tc.expectedMismatches,
				expectedDirOnly:    tc.expectedDirOnly,
				expectedIsNegate:   true,
			}
		},
	)
	runTestSet_ignorePattern(t, testCases)
}

func Test_createIgnorePattern_dirOnly(t *testing.T) {
	testCases := mapSlice(testCases_ignorePatternBasic,
		func(tc testCase_ignorePattern) testCase_ignorePattern {
			return testCase_ignorePattern{
				name:               tc.name,
				pattern:            tc.pattern + "/",
				expectedMatches:    tc.expectedMatches,
				expectedMismatches: tc.expectedMismatches,
				expectedDirOnly:    true,
				expectedIsNegate:   tc.expectedIsNegate,
			}
		},
	)
	runTestSet_ignorePattern(t, testCases)
}

func Test_createIgnorePattern_trailingSpace(t *testing.T) {
	testCases := mapSlice(testCases_ignorePatternBasic,
		func(tc testCase_ignorePattern) testCase_ignorePattern {
			return testCase_ignorePattern{
				name:               tc.name,
				pattern:            tc.pattern + strings.Repeat(" ", 1+len(tc.name)%3),
				expectedMatches:    tc.expectedMatches,
				expectedMismatches: tc.expectedMismatches,
				expectedDirOnly:    tc.expectedDirOnly,
				expectedIsNegate:   tc.expectedIsNegate,
			}
		},
	)
	runTestSet_ignorePattern(t, testCases)
}

// NOTE: For a new test that is not based on basic set the below snippet can be used.
// func Test_createIgnorePattern_someNewTest(t *testing.T) {
// 	testCases := []testCase_ignorePattern{
// 		{...}
// 	}
// 	runTestSet_ignorePattern(t, testCases)
// }

type testCase_ignoreFilter struct {
	// Test name.
	name string
	// Ignore patterns.
	patterns []string
	// Files that are expected to be ignored/copied during copy.
	// Make sure all of them are able to exist within a single FS (the same name should not
	// refer to a file and a directory).
	expectedIgnored []string
	expectedCopied  []string
}

func runTestSet_ignoreFilter(t *testing.T, testCases []testCase_ignoreFilter) {
	// Helper function to create mock FS for the testcase
	createFS := func(tc testCase_ignoreFilter) fs.FS {
		fsys := fstest.MapFS{}
		if tc.patterns != nil {
			fsys[ignoreFile] = &fstest.MapFile{
				Data: []byte(strings.Join(tc.patterns, "\n")),
				Mode: fs.FileMode(0644),
			}
		}
		for _, name := range slices.Concat(tc.expectedCopied, tc.expectedIgnored) {
			fsys[name] = &fstest.MapFile{
				Mode: fs.FileMode(0644),
			}
		}
		return fsys
	}

	basedst := t.TempDir()

	// Do test
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := createFS(tc)

			filter, err := ignoreFilter(fsys, ignoreFile)
			assert.Nil(t, err)
			assert.NotNil(t, filter)

			dst := filepath.Join(basedst, tc.name)
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
			for _, name := range tc.expectedIgnored {
				assert.NoFileExists(t, path.Join(dst, name))
			}
			for _, name := range tc.expectedCopied {
				assert.FileExists(t, path.Join(dst, name))
			}
		})
	}
}

// Prepare basic test set for ignoreFilter function
var testCases_ignoreFilterBasic = mapSlice(ignoreTestData_names,
	func(td ignoreTestData) testCase_ignoreFilter {
		return testCase_ignoreFilter{
			name: td.name,
			patterns: []string{
				td.pattern,
			},
			// Expand with some meaningful paths (td.matches itself has no path-item).
			expectedIgnored: append(td.matches,
				"in_subdir/"+td.matches[0],
				"in/deep/nested/subdir/"+td.matches[0],
				"as_subdir/"+td.matches[0]+"/bar",
				"as_subdir/"+td.matches[0]+"/with_nested_subdir/bar",
				"as/deep/nested/subdir/"+td.matches[0]+"/bar",
				"as/deep/nested/subdir/"+td.matches[0]+"/with_nested_subdir/bar",
			),
			// Expand with some meaningful paths (td.mismatches itself has no path-item).
			expectedCopied: append(td.mismatches,
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
	runTestSet_ignoreFilter(t, testCases_ignoreFilterBasic)
}

func Test_ignoreFilter_singleNegate(t *testing.T) {
	// Single negate pattern has no effect.
	testCases := mapSlice(testCases_ignoreFilterBasic,
		func(tc testCase_ignoreFilter) testCase_ignoreFilter {
			return testCase_ignoreFilter{
				name: tc.name,
				patterns: []string{
					"!" + tc.patterns[0],
				},
				expectedIgnored: nil,
				expectedCopied:  slices.Concat(tc.expectedCopied, tc.expectedIgnored),
			}
		})
	runTestSet_ignoreFilter(t, testCases)
}

func Test_ignoreFilter_selfNegate(t *testing.T) {
	// An ignore pattern followed by the same but negated (thus it just reinclude all).
	testCases := mapSlice(testCases_ignoreFilterBasic,
		func(tc testCase_ignoreFilter) testCase_ignoreFilter {
			return testCase_ignoreFilter{
				name: tc.name,
				patterns: []string{
					tc.patterns[0],
					"!" + tc.patterns[0],
				},
				expectedIgnored: nil,
				expectedCopied:  slices.Concat(tc.expectedCopied, tc.expectedIgnored),
			}
		})
	runTestSet_ignoreFilter(t, testCases)
}

func Test_ignoreFilter_negateWrongOrder(t *testing.T) {
	// An ignore pattern followed by the same but negated (thus it just reinclude all).
	testCases := mapSlice(testCases_ignoreFilterBasic,
		func(tc testCase_ignoreFilter) testCase_ignoreFilter {
			return testCase_ignoreFilter{
				name: tc.name,
				patterns: []string{
					"!" + tc.patterns[0],
					tc.patterns[0],
				},
				expectedIgnored: tc.expectedIgnored,
				expectedCopied:  tc.expectedCopied,
			}
		})
	runTestSet_ignoreFilter(t, testCases)
}

func Test_ignoreFilter_singleDirOnly(t *testing.T) {
	// Generate test set from the common test data rather than from the ignoreFilter basic set
	// because expectations in this case differ significantly.
	testCases := mapSlice(ignoreTestData_names,
		func(td ignoreTestData) testCase_ignoreFilter {
			return testCase_ignoreFilter{
				name: td.name,
				patterns: []string{
					td.pattern + "/",
				},
				// Expand with some meaningful paths (td.matches itself has no path-item).
				expectedIgnored: []string{
					td.matches[0] + "/as_dir",
					td.matches[0] + "/as_dir_with_nested_subdir/bar",
					"as_subdir/" + td.matches[0] + "/bar",
					"as_subdir/" + td.matches[0] + "/with_nested_subdir/bar",
					"as/deep/nested/subdir/" + td.matches[0] + "/bar",
					"as/deep/nested/subdir/" + td.matches[0] + "/with_nested_subdir/bar",
				},
				// Expand with some meaningful paths (td.mismatches itself has no path-item).
				expectedCopied: slices.Concat(
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
	runTestSet_ignoreFilter(t, testCases)
}

func Test_ignoreFilter_multiNames(t *testing.T) {
	testCases := []testCase_ignoreFilter{
		{
			name: "any",
			patterns: []string{
				"name1",
				"name2",
			},
			expectedIgnored: []string{
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
			expectedCopied: []string{
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
			expectedIgnored: []string{
				"as_subdir/name1/foo",
				"as/deep/nested/subdir/name1/bar",
				"as_subdir/name2/foo",
				"as/deep/nested/subdir/name2/bar",
			},
			expectedCopied: []string{
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
			expectedIgnored: []string{
				"name1",
				"in_subdir/name1",
				"in/deep/nested/subdir/name1",
				"as_subdir/name1/foo",
				"as/deep/nested/subdir/name1/bar",
				"as_subdir/name2/bar",
				"as/deep/nested/subdir/name2/bar",
			},
			expectedCopied: []string{
				"name2",
				"in_subdir/name2",
				"in/deep/nested/subdir/name2",
				"name3",
				"name4",
			},
		},
	}
	runTestSet_ignoreFilter(t, testCases)
}

func Test_ignoreFilter_fixedDepth(t *testing.T) {
	testCases := []testCase_ignoreFilter{
		{
			name: "name_at_depth1",
			patterns: []string{
				"*/foo",
			},
			expectedIgnored: []string{
				"in_subdir/foo",
				"in_another_subdir/foo",
				"as_subdir/foo/bar",
				"as_another_subdir/foo/bar",
			},
			expectedCopied: []string{
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
			expectedIgnored: []string{
				"in_subdir/of_depth2/foo",
				"in_another_subdir/of_depth2/foo",
				"as_subdir/of_depth2/foo/bar",
				"as_another_subdir/of_depth2/foo/bar",
			},
			expectedCopied: []string{
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
			expectedIgnored: []string{
				"foo/bar",
				"foo/blabla",
				"foo/with_subdir/bar",
				"foo/with_subdir/blabla",
			},
			expectedCopied: []string{
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
			expectedIgnored: []string{
				"foo/subdir/bar",
				"foo/subdir/blabla",
				"foo/another_subdir/bar",
			},
			expectedCopied: []string{
				"as_subdir/foo/subdir/bar",
				"as/subdir/of/another/depth/foo/subdir/bar",
				"foo/bar",
				"foo/blabla",
				"foo2/subdir/bar",
			},
		},
	}
	runTestSet_ignoreFilter(t, testCases)
}

func Test_ignoreFilter_reinclude(t *testing.T) {
	testCases := []testCase_ignoreFilter{
		{
			name: "by_name",
			patterns: []string{
				"*name?",
				"!renamed",
			},
			expectedIgnored: []string{
				"name1",
				"in_subdir/filename2",
				"in/deep/nested/subdir/rename3",
				"as_subdir/dirname4/bar",
				"as_subdir/dirname4/renamed",
				"as/deep/nested/subdir/newname5/bar",
				"as/deep/nested/subdir/newname5/renamed",
			},
			expectedCopied: []string{
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
			expectedIgnored: []string{
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
			expectedCopied: []string{
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
			expectedIgnored: []string{
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
			expectedCopied: []string{
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
	runTestSet_ignoreFilter(t, testCases)
}
