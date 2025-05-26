package pack

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ignorePattern corresponds to a single ignore pattern from .packignore file.
type ignorePattern struct {
	// re holds the "matching" part of ignore pattern (i.e. w/o trailing spaces, directory and
	// negate markers) in the form of regular expression.
	re *regexp.Regexp
	// dirOnly defines whether this pattern should be applied to all entries (false) or
	// to directory entries only (true).
	dirOnly bool
	// isNegate defines how to interpret the match (false means that it's an ordinary pattern
	// that excludes entry, true - it's a negate pattern that includes entry).
	isNegate bool
}

func turnEscapedToHexCode(s string, c rune) string {
	return strings.ReplaceAll(s, `\`+string(c), fmt.Sprintf(`\x%x`, c))
}

func splitIgnorePattern(pattern string) (cleanPattern string, dirOnly, isNegate bool) {
	// Remove trailing spaces (unless escaped one).
	cleanPattern = turnEscapedToHexCode(pattern, ' ')
	cleanPattern = strings.TrimRight(cleanPattern, " ")
	// Parse negate and directory markers.
	cleanPattern, dirOnly = strings.CutSuffix(cleanPattern, "/")
	cleanPattern, isNegate = strings.CutPrefix(cleanPattern, "!")
	return
}

func createIgnorePattern(pattern, basepath string) (ignorePattern, error) {
	// First, get rid of `\\` to simplify further handling of escaped sequences.
	// From now on any `\c` always means escaped 'c' (previously it might also
	// occur as a part of `\\c` sequence which denotes '\' followed by <c>).
	pattern = turnEscapedToHexCode(pattern, '\\')

	cleanPattern, dirOnly, isNegate := splitIgnorePattern(pattern)

	// Translate pattern to regex expression.
	expr := cleanPattern
	// Turn escaped '*' and '?' to their hex representation to simplify the translation.
	expr = turnEscapedToHexCode(expr, '*')
	expr = turnEscapedToHexCode(expr, '?')
	// Escape symbols that designate themselves in pattern, but have special meaning in regex.
	for _, s := range []string{"(", ")", "{", "}", "+"} {
		// Do unescape first to avoid double escaping of the ones that are already escaped.
		expr = strings.ReplaceAll(expr, "\\"+s, s)
		expr = strings.ReplaceAll(expr, s, "\\"+s)
	}
	// Replace wildcards with the corresponding regex representation.
	// Note that '{0,}' (not '*') is used while replacing '**' to avoid confusing
	// in the subsequent replacement of a single '*'.
	expr = strings.ReplaceAll(expr, "/**/", "/([^/]+/){0,}")
	expr, found := strings.CutPrefix(expr, "**/")
	if found || !strings.Contains(cleanPattern, "/") {
		expr = "([^/]+/){0,}" + expr
	}
	expr, found = strings.CutSuffix(expr, "/**")
	if found {
		expr = expr + "/([^/]+/){0,}[^/]+"
	}
	expr = strings.ReplaceAll(expr, "*", "[^/]*")
	expr = strings.ReplaceAll(expr, "?", "[^/]")

	re, err := regexp.Compile("^" + basepath + expr + "$")
	if err != nil {
		return ignorePattern{}, fmt.Errorf("failed to compile expression: %w", err)
	}

	return ignorePattern{
		re:       re,
		dirOnly:  dirOnly,
		isNegate: isNegate,
	}, nil
}

// loadIgnorePatterns reads ignore patterns from the patternsFile.
func loadIgnorePatterns(fsys fs.FS, patternsFile string) ([]ignorePattern, error) {
	contents, err := fs.ReadFile(fsys, patternsFile)
	if err != nil {
		return nil, err
	}

	basepath, _ := filepath.Split(patternsFile)

	var patterns []ignorePattern
	s := bufio.NewScanner(bytes.NewReader(contents))
	for s.Scan() {
		pattern := s.Text()
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}

		p, err := createIgnorePattern(pattern, basepath)
		if err != nil {
			return nil, err
		}

		patterns = append(patterns, p)
	}
	return patterns, nil
}

// ignoreFilter returns filter function that implements .gitignore approach of filtering files.
func ignoreFilter(fsys fs.FS, patternsFile string) (skipFilter, error) {
	patterns, err := loadIgnorePatterns(fsys, patternsFile)
	if err != nil {
		return nil, err
	}

	// According to .gitignore documentation "the last matching pattern decides the outcome"
	// so we need to iterate in reverse order until the first match.
	slices.Reverse(patterns)

	return func(srcInfo os.FileInfo, src string) bool {
		// Skip ignore file itself.
		if src == patternsFile {
			return true
		}
		for _, p := range patterns {
			isApplicable := srcInfo.IsDir() || !p.dirOnly
			if isApplicable && p.re.MatchString(src) {
				return !p.isNegate
			}
		}
		return false
	}, nil
}
