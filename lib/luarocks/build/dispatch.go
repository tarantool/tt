package build

import (
	"context"
	"fmt"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// RunBackend dispatches to the build backend selected by spec.Build.Type.
//
// Supported types:
//
//   - ""        → builtin (upstream default for format ≥ 3.0)
//   - "builtin" → builtin
//   - "cmake"   → cmake
//   - "make"    → make
//   - "command" → command
//   - "none"    → no-op (returns nil immediately)
//
// Any other value yields ErrUnsupportedRockspecFeature wrapped with the
// observed type string (typed errors callers can branch on).
//
// srcDir is the unpacked source tree the rock will be built against.
// destDir is the staging root the backend writes outputs into. The facade
// is responsible for orchestrating srcDir (post-fetch) and destDir
// (the `<tree>/.../build/` working area handed to tree.Deploy).
func RunBackend(ctx context.Context, spec *rocks.Rockspec, srcDir, destDir string, cfg rocks.Config) error {
	switch spec.Build.Type {
	case "", "builtin":
		return runBuiltin(ctx, spec, srcDir, destDir, cfg)
	case "cmake":
		return runCMake(ctx, spec, srcDir, destDir, cfg)
	case "make":
		return runMake(ctx, spec, srcDir, destDir, cfg)
	case "command":
		return runCommand(ctx, spec, srcDir, destDir, cfg)
	case "none":
		return nil
	default:
		return fmt.Errorf("build: type %q: %w", spec.Build.Type, rocks.ErrUnsupportedRockspecFeature)
	}
}
