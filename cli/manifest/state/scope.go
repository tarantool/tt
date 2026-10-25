// Package state owns tt's on-disk install state: where a scope puts a package's
// files, and the per-package metadata tt records under manifests/<pkg>/.
//
// Install writes that state, list reads it and uninstall unwinds it, so the
// three commands must agree on the layout down to the directory name. Keeping
// it in one package is what makes that agreement checkable instead of
// duplicated: a scope's directories are resolved once, by ResolveLayout, and
// the metadata file names are constants nobody re-spells.
//
// The dependency ledger uninstall stands on lives here too, and it is derived
// rather than stored: a dependency is owned by every installed package whose
// lock pins it, so removing a package's manifests/<pkg>/ directory is what
// decrements the count. Nothing has to be kept in sync by hand, and a
// half-written install cannot corrupt a counter that does not exist.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Scope is where tt package install lays a package down, and where list and
// uninstall look for it. The three scopes differ in install-root and in the
// on-disk layout Tarantool's loaders expect.
type Scope string

const (
	// ScopeProject installs into <cwd>/.rocks/, the self-contained deployment
	// tree. It is the default.
	ScopeProject Scope = "project"
	// ScopeUser installs into ~/.luarocks/, the personal LuaRocks tree.
	ScopeUser Scope = "user"
	// ScopeSystem installs into /usr/, the OS-package-style tree.
	ScopeSystem Scope = "system"
)

// ErrUnknownScope reports a --scope value that is not project, user or system.
var ErrUnknownScope = errors.New("unknown scope")

// ParseScope validates a --scope value, defaulting an empty string to project.
func ParseScope(raw string) (Scope, error) {
	switch Scope(raw) {
	case "", ScopeProject:
		return ScopeProject, nil
	case ScopeUser:
		return ScopeUser, nil
	case ScopeSystem:
		return ScopeSystem, nil
	default:
		return "", fmt.Errorf("%w %q (want project, user or system)", ErrUnknownScope, raw)
	}
}

// HasPrimary reports whether the scope can hold a primary package — the package
// the project itself develops, declared by app.manifest.toml in the install
// root. Only the project scope can: user and system trees are shared
// destinations with no project of their own, so everything in them is a guest.
func (scope Scope) HasPrimary() bool {
	return scope == ScopeProject
}

// Layout is the resolved set of directories one scope installs into.
type Layout struct {
	// Root is the install-root: <cwd> for project, the tree root otherwise.
	Root string
	// Tree is the rocks tree root the go-luarocks adapter reads and installs
	// into (<cwd>/.rocks for project, the root itself for user/system).
	Tree string
	// Share and Lib are the module directories a package's own files and its
	// dependencies land under, relative subtrees of the tree.
	Share string
	Lib   string
	// Manifests is where tt records per-package install metadata
	// (.rocks/manifests/<pkg>/) used by list/uninstall and dependency
	// refcounting.
	Manifests string
}

// ProjectLayout returns the layout of a project-shaped install root at dir:
// the project directory itself, or an installed version of an application in
// the user or system scope, which is laid out the same way. The rocks tree is
// dir/.rocks, Tarantool finds it from dir by its own loaders, and the
// per-package metadata of guests goes under dir/.rocks/manifests.
func ProjectLayout(dir string) Layout {
	tree := filepath.Join(dir, RocksDirName)

	return Layout{
		Root:      dir,
		Tree:      tree,
		Share:     filepath.Join(tree, shareTarantool),
		Lib:       filepath.Join(tree, libTarantool),
		Manifests: filepath.Join(tree, ManifestsDirName),
	}
}

// ResolveLayout maps a scope to its concrete directories. projectDir is the
// install-root for project scope (the caller's cwd); user and system derive
// their roots from the environment. The returned paths are absolute.
func ResolveLayout(scope Scope, projectDir string) (Layout, error) {
	switch scope {
	case ScopeProject:
		return ProjectLayout(projectDir), nil
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			var zero Layout

			return zero, fmt.Errorf("locating home directory: %w", err)
		}

		root := filepath.Join(home, ".luarocks")

		return Layout{
			Root:      root,
			Tree:      root,
			Share:     filepath.Join(root, shareLua),
			Lib:       filepath.Join(root, libLua),
			Manifests: filepath.Join(root, ManifestsDirName),
		}, nil
	case ScopeSystem:
		root := SystemRoot

		return Layout{
			Root:      root,
			Tree:      root,
			Share:     filepath.Join(root, shareTarantool),
			Lib:       filepath.Join(root, libTarantool),
			Manifests: filepath.Join(root, ManifestsDirName),
		}, nil
	default:
		var zero Layout

		return zero, fmt.Errorf("%w %q", ErrUnknownScope, scope)
	}
}

// Rocks-tree layout roots. The project and system scopes follow the Tarantool
// convention (share/tarantool, lib/tarantool); the user scope follows the
// LuaRocks 5.1 convention.
const (
	// RocksDirName is the project scope's tree directory, and the prefix archive
	// entries carry.
	RocksDirName = ".rocks"
	// ManifestsDirName is the per-package metadata directory inside a tree. It is
	// a reserved package name for exactly this reason.
	ManifestsDirName = "manifests"
	shareTarantool   = "share/tarantool"
	libTarantool     = "lib/tarantool"
	shareLua         = "share/lua/5.1"
	libLua           = "lib/lua/5.1"
	// SystemRoot is the OS-package install-root.
	SystemRoot = "/usr"
)
