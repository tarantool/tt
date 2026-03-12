package pack

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type ignoreFilter struct {
	patternsFileName string
	rootNode         *ignoreFilterNode
}

func createIgnoreFilter(fsys fs.FS, rootDir, patternsFileName string) (*ignoreFilter, error) {
	patternsFile := filepath.Join(rootDir, patternsFileName)
	rootNode, err := createIgnoreFilterNode(fsys, patternsFile, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create root node: %w", err)
	}

	return &ignoreFilter{
		patternsFileName: patternsFileName,
		rootNode:         rootNode,
	}, nil
}

func (f ignoreFilter) shouldSkip(info os.FileInfo, path string) bool {
	if !strings.HasPrefix(path, f.rootNode.dir) {
		return false
	}

	// Skip ignore pattern file itself.
	if !info.IsDir() && info.Name() == f.patternsFileName {
		return true
	}

	// Find the deepest node that is applicable to path.
	node := f.rootNode.findNode(path)

	return node.shouldSkip(info, path)
}

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

func splitIgnorePattern(pattern string) (matchingPattern string, dirOnly, isNegate bool) {
	// Remove trailing spaces (unless escaped one).
	matchingPattern = turnEscapedToHexCode(pattern, ' ')
	matchingPattern = strings.TrimRight(matchingPattern, " ")
	// Parse negate and directory markers.
	matchingPattern, dirOnly = strings.CutSuffix(matchingPattern, "/")
	matchingPattern, isNegate = strings.CutPrefix(matchingPattern, "!")
	return
}

func createIgnorePattern(pattern, basepath string) (ignorePattern, error) {
	// First, get rid of `\\` to simplify further handling of escaped sequences.
	// From now on any `\c` always means escaped 'c' (previously it might also
	// occur as a part of `\\c` sequence which denotes '\' followed by <c>).
	pattern = turnEscapedToHexCode(pattern, '\\')

	matchingPattern, dirOnly, isNegate := splitIgnorePattern(pattern)

	// Translate matching pattern to regex expression.
	// Translation is performed step by step in a certain sequence (see comments).
	expr := matchingPattern

	// Turn escaped '*' and '?' to their hex representation to simplify the translation.
	expr = turnEscapedToHexCode(expr, '*')
	expr = turnEscapedToHexCode(expr, '?')
	// Escape symbols that designate themselves in pattern, but have special meaning in regex.
	for _, s := range []string{"(", ")", "{", "}", "+"} {
		// Do unescape first to avoid double escaping of the ones that are already escaped.
		expr = strings.ReplaceAll(expr, "\\"+s, s)
		expr = strings.ReplaceAll(expr, s, "\\"+s)
	}

	// Turn '?' to its regex representation here because '?' might be used in the subsequent
	// transformations to specify non-capturing groups '(?:re)'.
	expr = strings.ReplaceAll(expr, "?", "[^/]")

	// Replace '**' wildcards with the corresponding regex representation.
	// Note that '{0,}' rather than '*' is used while replacing '**' to avoid confusing
	// in the subsequent replacement of a single '*'.
	expr = strings.ReplaceAll(expr, "/**/", "/(?:[^/]+/){0,}")
	expr, found := strings.CutPrefix(expr, "**/")
	if found || !strings.Contains(matchingPattern, "/") {
		expr = "(?:[^/]+/){0,}" + expr
	}

	expr, found = strings.CutSuffix(expr, "/**")
	// Turn '*' to its regex representation before injecting basepath to avoid confusing with '*'
	// that basepath itself might contain (within basepath it's not a wildcard, but just '*').
	expr = strings.ReplaceAll(expr, "*", "[^/]*")

	// Construct final expression where the single captured group corresponds to the initial
	// matchingPattern. This captured group might be used additionally to identify if some path
	// matches pattern or not (see `ignorePattern.MatchPath`).
	if found {
		expr = fmt.Sprintf("(%s(?:/[^/]+){1,})", basepath+expr)
	} else {
		expr = fmt.Sprintf("(%s)(?:/[^/]+){0,}", basepath+expr)
	}

	re, err := regexp.Compile("^" + expr + "$")
	if err != nil {
		return ignorePattern{}, fmt.Errorf("failed to compile expression: %w", err)
	}

	return ignorePattern{
		re:       re,
		dirOnly:  dirOnly,
		isNegate: isNegate,
	}, nil
}

// MatchPath checks if path matches this pattern.
func (p ignorePattern) MatchPath(info fs.FileInfo, path string) bool {
	submatches := p.re.FindStringSubmatch(path)
	if submatches == nil {
		return false
	}

	// For dirOnly pattern it is needed to check additionally that if path is not a directory
	// it doesn't match directory part, which is represented with the single captured group
	// (see `createIgnorePattern`).
	return !p.dirOnly || info.IsDir() || submatches[1] != path
}

// loadIgnorePatterns reads ignore patterns from the patternsFile.
func loadIgnorePatterns(fsys fs.FS, patternsFile string) ([]ignorePattern, error) {
	contents, err := fs.ReadFile(fsys, patternsFile)
	if err != nil {
		return nil, err
	}

	basepath, _ := filepath.Split(patternsFile)

	// basepath is to be the part of every regex based on the patterns from this file,
	// thus escape symbols \(){}[]+?*| that designate themselves in a path, but have special
	// meaning in a regex.
	// Note that '\' is escaped first to avoid confusing with '\' introduced with escaping of
	// the rest ones.
	basepath = strings.ReplaceAll(basepath, "\\", "\\\\")
	basepath = regexp.MustCompile(`([(){}\[\]+?*|])`).ReplaceAllString(basepath, "\\$1")

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

type ignoreFilterNode struct {
	dir      string
	patterns []ignorePattern
	parent   *ignoreFilterNode
	children []*ignoreFilterNode
}

func createIgnoreFilterNode(fsys fs.FS, patternsFile string, parent *ignoreFilterNode) (
	*ignoreFilterNode,
	error,
) {
	patterns, err := loadIgnorePatterns(fsys, patternsFile)
	if err != nil {
		// There is a special condition for the root node that allows patternsFile to be missed
		// in a root-directory. The other nodes don't have this option since for all of them
		// construction is initiated by the presence of corresponding patternsFile.
		if parent != nil || !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to read patterns: %w", err)
		}
	}

	// According to .gitignore documentation "the last matching pattern decides the outcome"
	// so we need to iterate in reverse order until the first match.
	slices.Reverse(patterns)

	node := &ignoreFilterNode{
		dir:      filepath.Dir(patternsFile),
		patterns: patterns,
		parent:   parent,
		children: nil,
	}

	// Walk down the directories to find all the children.
	root := filepath.Dir(patternsFile)
	patternsFileName := filepath.Base(patternsFile)
	err = fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if d.Name() == patternsFileName && !d.IsDir() && path != patternsFile {
			child, err := createIgnoreFilterNode(fsys, path, node)
			if err != nil {
				return fmt.Errorf("failed to create node for %q: %w", path, err)
			}
			node.children = append(node.children, child)
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed walking %q: %w", root, err)
	}

	return node, nil
}

// findNode finds the deepest node that is applicable to path.
func (node *ignoreFilterNode) findNode(path string) *ignoreFilterNode {
	i := slices.IndexFunc(node.children, func(child *ignoreFilterNode) bool {
		return strings.HasPrefix(path, child.dir)
	})
	if i != -1 {
		return node.children[i].findNode(path)
	}

	return node
}

// shouldSkip searches the tree from this node towards root for the first pattern
// that matches path and returns true if it's regular one (i.e. non-negative)
// and false otherwise. If no match found it returns false as well.
func (node *ignoreFilterNode) shouldSkip(info fs.FileInfo, path string) bool {
	i := slices.IndexFunc(node.patterns, func(p ignorePattern) bool {
		return p.MatchPath(info, path)
	})
	if i != -1 {
		return !node.patterns[i].isNegate
	}

	if node.parent != nil {
		return node.parent.shouldSkip(info, path)
	}

	return false
}
