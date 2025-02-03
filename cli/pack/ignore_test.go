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

// Map is a generic slice mapper function.
func Map[T, V any](ts []T, fn func(T) V) []V {
	result := make([]V, len(ts))
	for i, t := range ts {
		result[i] = fn(t)
	}
	return result
}

// ignoreTestCase is used to define the very basic set of test cases
// which then used as a source to generate the actual testcases
// suitable for the corresponding test functions.
type ignoreTestCase struct {
	name       string
	pattern    string
	matches    []string
	mismatches []string
}

var ignoreTestCasesInAnyDir = []ignoreTestCase{
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

var ignoreTestCasesRelative = []ignoreTestCase{
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
		name:    "name_with_double_asterisk_between",
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

func Test_createIgnorePattern(t *testing.T) {
	type testCase struct {
		name       string
		pattern    string
		matches    []string
		mismatches []string
		dirOnly    bool
		isNegate   bool
	}

	runTestSet := func(t *testing.T, name string, testCases []testCase) {
		for _, tc := range testCases {
			t.Run(name+"_"+tc.name, func(t *testing.T) {
				p, err := createIgnorePattern(tc.pattern, "")
				assert.Nil(t, err)
				assert.NotNil(t, p.re)
				assert.Equal(t, tc.dirOnly, p.dirOnly)
				assert.Equal(t, tc.isNegate, p.isNegate)
				for _, s := range tc.matches {
					assert.Truef(t, p.re.MatchString(s),
						"%q doesn't match %s", s, p.re.String())
				}
				for _, s := range tc.mismatches {
					assert.False(t, p.re.MatchString(s),
						"%q matches %s", s, p.re.String())
				}
			})
		}
	}

	// Turn the global basic set into the basic set for this function.
	testCasesInAnyDir := Map(ignoreTestCasesInAnyDir,
		func(tc ignoreTestCase) testCase {
			return testCase{
				name:    tc.name,
				pattern: tc.pattern,
				matches: append(tc.matches,
					"in_subdir/"+tc.matches[0],
					"in/deep/nested/subdir/"+tc.matches[0],
				),
				mismatches: tc.mismatches,
				dirOnly:    false,
				isNegate:   false,
			}
		})
	testCasesRelative := Map(ignoreTestCasesRelative,
		func(tc ignoreTestCase) testCase {
			return testCase{
				name:       tc.name,
				pattern:    tc.pattern,
				matches:    tc.matches,
				mismatches: tc.mismatches,
				dirOnly:    false,
				isNegate:   false,
			}
		})
	testCasesBasic := slices.Concat(testCasesInAnyDir, testCasesRelative)

	// Run the basic set and a few simple variations (below).
	runTestSet(t, "basic", testCasesBasic)
	runTestSet(t, "negate", Map(testCasesBasic,
		func(tc testCase) testCase {
			return testCase{
				name:       tc.name,
				pattern:    "!" + tc.pattern,
				matches:    tc.matches,
				mismatches: tc.mismatches,
				dirOnly:    tc.dirOnly,
				isNegate:   true,
			}
		}))
	runTestSet(t, "dironly", Map(testCasesBasic,
		func(tc testCase) testCase {
			return testCase{
				name:       tc.name,
				pattern:    tc.pattern + "/",
				matches:    tc.matches,
				mismatches: tc.mismatches,
				dirOnly:    true,
				isNegate:   tc.isNegate,
			}
		}))
	runTestSet(t, "trailing_spaces", Map(testCasesBasic,
		func(tc testCase) testCase {
			return testCase{
				name:       tc.name,
				pattern:    tc.pattern + strings.Repeat(" ", 1+len(tc.name)%3),
				matches:    tc.matches,
				mismatches: tc.mismatches,
				dirOnly:    tc.dirOnly,
				isNegate:   tc.isNegate,
			}
		}))
}

func Test_ignoreFilter(t *testing.T) {
	type testCase struct {
		name     string
		patterns []string
		copied   []string
		ignored  []string
	}

	createFS := func(tc testCase) fs.FS {
		fsys := fstest.MapFS{}
		if tc.patterns != nil {
			fsys[ignoreFile] = &fstest.MapFile{
				Data: []byte(strings.Join(tc.patterns, "\n")),
				Mode: fs.FileMode(0644),
			}
		}
		for _, name := range slices.Concat(tc.copied, tc.ignored) {
			fsys[name] = &fstest.MapFile{
				Mode: fs.FileMode(0644),
			}
		}
		return fsys
	}

	runTestSet := func(t *testing.T, name string, dst string, testCases []testCase) {
		for _, tc := range testCases {
			t.Run(name+"_"+tc.name, func(t *testing.T) {
				fsys := createFS(tc)

				filter, err := ignoreFilter(fsys, ignoreFile)
				assert.Nil(t, err)
				assert.NotNil(t, filter)

				dst = filepath.Join(dst, name, tc.name)
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

	// No ignore file.
	t.Run("no ignore file", func(t *testing.T) {
		f, err := ignoreFilter(fstest.MapFS{}, ignoreFile)
		assert.NotNil(t, err)
		assert.True(t, errors.Is(err, fs.ErrNotExist))
		assert.Nil(t, f)
	})

	dst := t.TempDir()

	// Turn the global basic set into the basic set for this function.
	testCasesInAnyDir := Map(ignoreTestCasesInAnyDir,
		func(tc ignoreTestCase) testCase {
			return testCase{
				name: tc.name,
				patterns: []string{
					tc.pattern,
				},
				ignored: append(tc.matches,
					"in_subdir/"+tc.matches[0],
					"in/deep/nested/subdir/"+tc.matches[0],
					"as_subdir/"+tc.matches[0]+"/bar",
					"as/deep/nested/subdir/"+tc.matches[0]+"/bar",
				),
				copied: append(tc.mismatches,
					"in_subdir/"+tc.mismatches[0],
					"in/deep/nested/subdir/"+tc.mismatches[0],
				),
			}
		})
	// Need to split matches and mismatches into separate tests because they may contain elements
	// that cannot be represented in file system simultaneously (element refers to a file in one
	// slice and to a directory in another) but both need to be checked.
	var testCasesRelative []testCase
	testCasesRelative = append(testCasesRelative, Map(ignoreTestCasesRelative,
		func(tc ignoreTestCase) testCase {
			return testCase{
				name: tc.name,
				patterns: []string{
					tc.pattern,
				},
				ignored: tc.matches,
				copied:  nil,
			}
		})...)
	testCasesRelative = append(testCasesRelative, Map(ignoreTestCasesRelative,
		func(tc ignoreTestCase) testCase {
			return testCase{
				name: tc.name,
				patterns: []string{
					tc.pattern,
				},
				ignored: nil,
				copied:  tc.mismatches,
			}
		})...)
	testCasesBasic := slices.Concat(testCasesInAnyDir, testCasesRelative)

	// Run the basic set and a few simple variations (below).
	runTestSet(t, "basic", dst, testCasesBasic)

	// Single negate pattern has no effect.
	runTestSet(t, "single_negate", dst,
		Map(testCasesBasic, func(tc testCase) testCase {
			return testCase{
				name: tc.name,
				patterns: []string{
					"!" + tc.patterns[0],
				},
				ignored: nil,
				copied:  slices.Concat(tc.copied, tc.ignored),
			}
		}))

	// An ignore pattern followed by the same but negated (thus it just reinclude all).
	runTestSet(t, "selfnegate", dst,
		Map(testCasesBasic, func(tc testCase) testCase {
			return testCase{
				name: tc.name,
				patterns: []string{
					tc.patterns[0],
					"!" + tc.patterns[0],
				},
				ignored: nil,
				copied:  slices.Concat(tc.copied, tc.ignored),
			}
		}))

	// An ignore pattern preceded with the same but negated (thus negated one has no effect).
	runTestSet(t, "negate_wrong_order", dst,
		Map(testCasesBasic, func(tc testCase) testCase {
			return testCase{
				name: tc.name,
				patterns: []string{
					"!" + tc.patterns[0],
					tc.patterns[0],
				},
				ignored: tc.ignored,
				copied:  tc.copied,
			}
		}))

	// NOTE: Put a new `ignoreFilter`-test here.
	// Either add it into one of the existing sets or run the new one (make sure
	// the name of the set is unique).
	runTestSet(t, "multi_names", dst, []testCase{
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
	})

	runTestSet(t, "multi_patterns", dst, []testCase{
		{
			name: "reinclude",
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
				"name13",
				"rename14",
				"renamed",
				"in_subdir/renamed",
				"as_subdir/renamed/bar",
			},
		},
	})
}
