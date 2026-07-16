package build

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tarantool/tt/cli/manifest/version"
)

// versionLuaName is the descriptor file dropped into the package's own share
// namespace.
const versionLuaName = "version.lua"

// writeVersionLua generates version.lua at <tree>/share/tarantool/<package>/
// unless package.generate_version_lua = false. It is written under the package
// name, not a component namespace: it describes the package as a whole.
//
// A component that already laid a file at that exact path is a collision — two
// sources claiming version.lua — and is a hard error (exit code 1 at the CLI).
// laidOut is the set of destinations layoutComponent reported this run, so a
// stale version.lua left by a previous build does not count as a collision: it
// is simply overwritten.
func writeVersionLua(
	tree, pkgName string, generate bool, ver version.Version, builtAt time.Time,
	laidOut []string,
) error {
	if !generate {
		return nil
	}

	dst := filepath.Join(tree, shareTarantool, pkgName, versionLuaName)

	if slices.Contains(laidOut, dst) {
		return exitErrorf(exitStateError,
			"%w: component ships %s at %s, which the build also generates",
			errVersionLuaCollision, versionLuaName, dst)
	}

	content := version.GenerateVersionLua(ver, builtAt)

	mkErr := os.MkdirAll(filepath.Dir(dst), dirPerm)
	if mkErr != nil {
		return fmt.Errorf("creating package share dir: %w", mkErr)
	}

	writeErr := os.WriteFile(dst, []byte(content), filePerm)
	if writeErr != nil {
		return fmt.Errorf("writing %s: %w", versionLuaName, writeErr)
	}

	return nil
}
