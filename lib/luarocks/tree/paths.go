// Package tree implements the on-disk Tarantool-rocks layout: the path
// scheme under <tree>/share/tarantool, <tree>/lib/tarantool, <tree>/bin,
// and the Deploy operation that copies a built rock's artifacts into it.
//
// The layout is fixed for Tarantool:
//
//	<tree>/share/tarantool/rocks/<name>/<ver>/   — per-rock install dir
//	<tree>/share/tarantool/                       — deploy_lua_dir
//	<tree>/lib/tarantool/                         — deploy_lib_dir
//	<tree>/bin/                                   — bin scripts
//	<tree>/etc/                                   — conf files
//
// This package uses forward-slash Unix paths exclusively via
// path/filepath; no Windows-specific handling.
package tree

import "path/filepath"

// Paths computes the conventional subdirectories under a tree root.
//
// All methods are pure functions of Tree; they do not touch disk. The
// caller is responsible for ensuring directories exist (Tree.Open does
// the minimal mkdir).
type Paths struct {
	// Tree is the tree root (e.g. ".rocks" in a project, or a system
	// tarantool prefix). Always an absolute or project-relative path.
	Tree string
}

// RocksDir returns <tree>/share/tarantool/rocks — the rocks_dir upstream
// luarocks refers to.
func (p Paths) RocksDir() string {
	return filepath.Join(p.Tree, "share", "tarantool", "rocks")
}

// InstallDir returns <tree>/share/tarantool/rocks/<name>/<ver> — the
// per-rock install directory holding rockspec, rock_manifest, build/, doc/.
func (p Paths) InstallDir(name, ver string) string {
	return filepath.Join(p.RocksDir(), name, ver)
}

// DeployLuaDir returns <tree>/share/tarantool — where dotted .lua modules
// are flattened to slashed paths during Deploy.
func (p Paths) DeployLuaDir() string {
	return filepath.Join(p.Tree, "share", "tarantool")
}

// DeployLibDir returns <tree>/lib/tarantool — where compiled .so modules
// are flattened to slashed paths during Deploy.
func (p Paths) DeployLibDir() string {
	return filepath.Join(p.Tree, "lib", "tarantool")
}

// BinDir returns <tree>/bin — destination for build.install.bin entries.
func (p Paths) BinDir() string {
	return filepath.Join(p.Tree, "bin")
}

// ConfDir returns <tree>/etc — destination for build.install.conf entries.
func (p Paths) ConfDir() string {
	return filepath.Join(p.Tree, "etc")
}

// DocDir returns <tree>/share/tarantool/rocks/<name>/<ver>/doc — the
// per-rock documentation directory.
func (p Paths) DocDir(name, ver string) string {
	return filepath.Join(p.InstallDir(name, ver), "doc")
}
