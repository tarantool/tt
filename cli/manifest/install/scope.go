package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// Scope is where tt package install lays a package down. The three scopes
// differ in install-root and in the on-disk layout Tarantool's loaders expect;
// they also differ in what archive they accept (only project takes a with-deps
// archive).
type Scope string

const (
	// ScopeProject installs into <cwd>/.rocks/, the self-contained deployment
	// tree. It is the default and the only scope that accepts a with-deps
	// archive (the bundled _runtime/ has no place in a user or system tree).
	ScopeProject Scope = "project"
	// ScopeUser installs into ~/.luarocks/, the personal LuaRocks tree. It
	// accepts only a --without-deps archive.
	ScopeUser Scope = "user"
	// ScopeSystem installs into /usr/, the OS-package-style tree. It needs root
	// and accepts only a --without-deps archive.
	ScopeSystem Scope = "system"
)

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
		return "", fmt.Errorf("%w %q (want project, user or system)", errUnknownScope, raw)
	}
}

// AcceptsWithDeps reports whether the scope may receive a with-deps archive.
// Only project does: user and system are shared LuaRocks trees where bundling a
// runtime is meaningless, so a with-deps archive is rejected before any write.
func (scope Scope) AcceptsWithDeps() bool {
	return scope == ScopeProject
}

// layout is the resolved set of directories one scope installs into.
type layout struct {
	// root is the install-root: <cwd> for project, the tree root otherwise.
	root string
	// tree is the rocks tree root the go-luarocks adapter reads and installs
	// into (<cwd>/.rocks for project, the root itself for user/system).
	tree string
	// share and lib are the module directories a package's own files and its
	// dependencies land under, relative subtrees of the tree.
	share string
	lib   string
	// manifests is where tt records per-package install metadata
	// (.rocks/manifests/<pkg>/) used by list/uninstall and dependency
	// refcounting.
	manifests string
}

// resolveLayout maps a scope to its concrete directories. projectDir is the
// install-root for project scope (the caller's cwd); user and system derive
// their roots from the environment. The returned paths are absolute.
func resolveLayout(scope Scope, projectDir string) (layout, error) {
	switch scope {
	case ScopeProject:
		tree := filepath.Join(projectDir, rocksDirName)

		return layout{
			root:      projectDir,
			tree:      tree,
			share:     filepath.Join(tree, shareTarantool),
			lib:       filepath.Join(tree, libTarantool),
			manifests: filepath.Join(tree, manifestsDirName),
		}, nil
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			var zero layout

			return zero, fmt.Errorf("locating home directory: %w", err)
		}

		root := filepath.Join(home, ".luarocks")

		return layout{
			root:      root,
			tree:      root,
			share:     filepath.Join(root, shareLua),
			lib:       filepath.Join(root, libLua),
			manifests: filepath.Join(root, manifestsDirName),
		}, nil
	case ScopeSystem:
		root := systemRoot

		return layout{
			root:      root,
			tree:      root,
			share:     filepath.Join(root, shareTarantool),
			lib:       filepath.Join(root, libTarantool),
			manifests: filepath.Join(root, manifestsDirName),
		}, nil
	default:
		var zero layout

		return zero, fmt.Errorf("%w %q", errUnknownScope, scope)
	}
}

// Rocks-tree layout roots. The project and system scopes follow the Tarantool
// convention (share/tarantool, lib/tarantool); the user scope follows the
// LuaRocks 5.1 convention.
const (
	rocksDirName     = ".rocks"
	manifestsDirName = "manifests"
	shareTarantool   = "share/tarantool"
	libTarantool     = "lib/tarantool"
	shareLua         = "share/lua/5.1"
	libLua           = "lib/lua/5.1"
	// systemRoot is the OS-package install-root.
	systemRoot = "/usr"
)
