// Package state owns tt's on-disk install state: where each scope puts what
// tt package install unpacks, and the metadata list and uninstall read back.
//
// Install writes that state, list reads it and uninstall unwinds it, so the
// three commands must agree on the layout down to the directory name. Keeping
// it in one package is what makes that agreement checkable instead of
// duplicated: the directories are named once, here, and the metadata file
// names are constants nobody re-spells.
//
// The scopes hold two different shapes.
//
// The project scope is one install root, the project directory. The project's
// own package is declared by app.manifest.toml at its top, and every package
// installed into it is a guest: its files join the shared .rocks/ tree and its
// manifest, lock and VERSION are recorded under .rocks/manifests/<pkg>/. The
// dependency ledger uninstall stands on is derived from those records rather
// than stored: a dependency is owned by every installed package whose lock
// pins it, so removing a package's manifests/<pkg>/ directory is what
// decrements the count. Nothing has to be kept in sync by hand, and a
// half-written install cannot corrupt a counter that does not exist.
//
// The user and system scopes hold applications, laid out the way tt binary
// lays out its products and under the same root (internal/scope), so an
// application finds the tarantool installed next to it and tt instance finds
// both (decision 8):
//
//	<root>/
//	├── bin/, include/                  active links of tt binary
//	├── apps/<app> -> ../versions/<app>/<app>_<ver>
//	└── versions/
//	    ├── tarantool/tarantool_<ver>/   installations of tt binary
//	    └── <app>/<app>_<ver>/           an installed version of an application
//
// Each version is an install root of its own, laid out like a project
// (ProjectLayout): the package's manifest is its primary manifest, at its
// top, and its rocks are in its own .rocks/. Versions share nothing with each
// other or with other applications, so there is no ledger there: a version
// goes with everything in it. apps/<app> selects the active version. Runtime
// files of instances, under <root>/var/, and host files, under <root>/etc/,
// are outside versions/ and belong to tt instance; tt package only reads the
// pid files to tell whether an application runs (see versions.go).
package state

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Scope is where tt package install lays a package down, and where list and
// uninstall look for it.
type Scope string

const (
	// ScopeProject installs into the project directory, the self-contained
	// deployment tree: a guest package joins its .rocks/. It is the default.
	ScopeProject Scope = "project"
	// ScopeUser installs application versions into the user root,
	// TT_USER_ROOT or ~/.local/share/tarantool by default.
	ScopeUser Scope = "user"
	// ScopeSystem installs application versions into the system root,
	// TT_SYSTEM_ROOT or /opt/tarantool by default.
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

// Layout is the set of directories of one project-shaped install root.
type Layout struct {
	// Root is the install root: the project directory, or an installed
	// version of an application.
	Root string
	// Tree is the rocks tree root the go-luarocks adapter reads and installs
	// into, <root>/.rocks.
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

// Rocks-tree layout, the Tarantool convention for a project's .rocks/.
const (
	// RocksDirName is the project scope's tree directory, and the prefix archive
	// entries carry.
	RocksDirName = ".rocks"
	// ManifestsDirName is the per-package metadata directory inside a tree. It is
	// a reserved package name for exactly this reason.
	ManifestsDirName = "manifests"
	shareTarantool   = "share/tarantool"
	libTarantool     = "lib/tarantool"
)
