package build

import (
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/tarantool/tt/cli/manifest"
)

// defaultInclude is the include set applied when a component declares none: the
// pure-Lua sources and any pre-built shared objects that ship in the component
// tree. A component that declares its own include list replaces this default
// wholesale, rather than adding to it.
func defaultInclude() []string {
	return []string{"*.lua", "*.so"}
}

// defaultExclude is always applied, ahead of the component's own exclude list.
// These entries are packaging hazards or tt-internal state that must never land
// in a component regardless of what the manifest says: build scratch, the rocks
// tree, the runtime bundle, vendored deps, VCS/hidden files and the manifest
// itself. A component's exclude patterns extend this list; they do not replace
// it, so no manifest can accidentally ship .rocks/ or app.manifest.toml.
func defaultExclude() []string {
	return []string{
		"test/",
		"tests/",
		"_build/",
		".rocks/",
		"_runtime/",
		"vendor/",
		".*", // Hidden files and directories (.git, .rocks is also caught above).
		manifestFileName,
	}
}

// fileFilter decides which files of a component are laid out. A file is kept
// when it matches the include allowlist and is not caught by the exclude
// denylist; directories are consulted only for pruning (excluded subtrees are
// skipped whole). Both matchers use gitignore syntax evaluated from the
// component's path root.
type fileFilter struct {
	include gitignore.Matcher
	exclude gitignore.Matcher
}

// newFileFilter builds the filter for one component: its include list (or the
// default when empty) as the allowlist, and defaultExclude followed by the
// component's own exclude patterns as the denylist. Patterns are anchored at
// the component root (a nil gitignore domain).
func newFileFilter(component manifest.Component) fileFilter {
	include := component.Include
	if len(include) == 0 {
		include = defaultInclude()
	}

	// defaultExclude returns a fresh slice, so appending the component's own
	// patterns onto it neither mutates the defaults nor drops them.
	exclude := append(defaultExclude(), component.Exclude...)

	return fileFilter{
		include: gitignore.NewMatcher(parsePatterns(include)),
		exclude: gitignore.NewMatcher(parsePatterns(exclude)),
	}
}

// parsePatterns parses each gitignore line at the tree root (nil domain).
func parsePatterns(lines []string) []gitignore.Pattern {
	out := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		out = append(out, gitignore.ParsePattern(line, nil))
	}

	return out
}

// keepFile reports whether the file at rel (path components relative to the
// component root) is laid out: included by the allowlist and not excluded.
func (f fileFilter) keepFile(rel []string) bool {
	return f.include.Match(rel, false) && !f.exclude.Match(rel, false)
}

// pruneDir reports whether the directory at rel is an excluded subtree that the
// walk should skip entirely. The include allowlist never prunes directories — a
// directory itself rarely matches a file glob like *.lua, yet its files may —
// so only the exclude denylist gates pruning.
func (f fileFilter) pruneDir(rel []string) bool {
	return f.exclude.Match(rel, true)
}
