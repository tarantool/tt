package app

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/tarantool/tt/v3/internal/scope"
)

// Names returns the names of the applications a target may name with opts,
// for the completion of a target: the project the command runs in, unless
// opts.Scope is user or system, and then the applications installed in the
// system scope, or in the scope opts.Scope names, in the order of their
// names. A name comes once, the project's first.
//
// These are the applications Load finds without --scope, or with the scope
// opts.Scope names; the user scope is offered only for --scope user, as Load
// looks there only then. Names finds what it can and reports nothing: a
// project whose manifest does not parse, or a scope root that cannot be
// read, offers no name.
func Names(opts Options) []string {
	var names []string

	if opts.Scope == "" || opts.Scope == ScopeProject {
		name, found := currentProjectName(opts)
		if found {
			names = append(names, name)
		}
	}

	installedScope := opts.Scope
	if installedScope == "" {
		installedScope = ScopeSystem
	}

	if installedScope != ScopeUser && installedScope != ScopeSystem {
		return names
	}

	for _, name := range installedNames(installedScope) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names
}

// currentProjectName returns the name of the project the search for a
// project finds from opts.Start, if it finds one that names itself.
func currentProjectName(opts Options) (string, bool) {
	start := opts.Start
	if start == "" {
		start = "."
	}

	projectDir, found, err := scope.FindProject(start)
	if err != nil || !found {
		return "", false
	}

	name, err := projectName(projectDir, opts.Integrity)
	if err != nil {
		return "", false
	}

	return name, true
}

// installedNames returns the names of the applications installed in scp, the
// user or the system scope, in their order: the entries of <root>/apps/ that
// are directories, or links to them, and that a target can name.
func installedNames(scp Scope) []string {
	root, err := scopeRoot(scp)
	if err != nil {
		return nil
	}

	appsDir := filepath.Join(root, appsDirName)

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		name := entry.Name()
		if !validName(name) {
			continue
		}

		info, err := os.Stat(filepath.Join(appsDir, name))
		if err != nil || !info.IsDir() {
			continue
		}

		names = append(names, name)
	}

	return names
}
