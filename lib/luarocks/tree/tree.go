package tree

import (
	"errors"
	"fmt"
	"os"

	rocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/manif"
)

// File and directory permission bits used across the tree package when
// materializing deployed rocks.
const (
	// dirPerm is applied to directories created in the rock tree.
	dirPerm os.FileMode = 0o750
	// filePerm is the default mode for regular deployed files (non-executable).
	filePerm os.FileMode = 0o644
	// execPerm is the mode for executable deployed files: install.bin scripts
	// and the generated command wrappers.
	execPerm os.FileMode = 0o755
)

// Tree is the in-memory handle on a Tarantool rocks tree on disk. It
// composes the path scheme (Paths) and a ManifestStore. Operations
// that mutate the tree — Deploy chiefly — go through this type.
type Tree struct {
	Paths

	// Store reads/writes the tree-level and per-rock manifest files. The
	// default is manif.FileStore. Tests inject fakes.
	Store rocks.ManifestStore

	// BinWrap, when non-nil, makes Deploy reproduce upstream LuaRocks'
	// command-script handling (repos.deploy_files → fs.wrap_script): the real
	// script is installed under the per-rock dir and the public <tree>/bin entry
	// becomes a generated /bin/sh launcher. When nil, build.install.bin entries
	// are copied verbatim (the pre-parity behavior; kept for callers that
	// construct a Tree without a Tarantool target).
	BinWrap *BinWrap
}

// BinWrap carries the two environment-dependent inputs upstream bakes into a
// command wrapper (fs/unix.lua wrap_script): the absolute interpreter path
// (cfg.variables.LUA_BINDIR + cfg.lua_interpreter) and cfg.sysconfdir.
type BinWrap struct {
	// Interpreter is the absolute path to the Lua interpreter the wrapper
	// execs, e.g. "/opt/tarantool/bin/tarantool".
	Interpreter string
	// Sysconfdir is the value exported as LUAROCKS_SYSCONFDIR, matching
	// upstream's cfg.sysconfdir (default "/etc/luarocks").
	Sysconfdir string
}

// Open returns a Tree handle backed by cfg.Tree on disk. The minimal set
// of subdirectories — RocksDir, DeployLuaDir, DeployLibDir, BinDir — are
// created if missing (the tree-init step is idempotent).
//
// cfg.Tarantool fields are not consulted: tree management is independent
// of the Tarantool target (which only matters at build time).
func Open(cfg rocks.Config) (*Tree, error) {
	if cfg.Tree == "" {
		return nil, errors.New("tree.Open: Config.Tree is empty")
	}

	t := &Tree{
		Paths: Paths{Tree: cfg.Tree},
		Store: manif.FileStore{},
	}
	for _, d := range []string{t.RocksDir(), t.DeployLuaDir(), t.DeployLibDir(), t.BinDir()} {
		err := os.MkdirAll(d, dirPerm)
		if err != nil {
			return nil, fmt.Errorf("tree.Open: mkdir %q: %w", d, err)
		}
	}

	return t, nil
}
