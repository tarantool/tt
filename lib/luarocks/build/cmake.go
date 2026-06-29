package build

import (
	"context"
	"sort"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// runCMake implements the `cmake` build backend.
//
// Sequence (pure pass-through — luarocks itself does NOT auto-inject
// LUA_INCLUDE_DIR, CMAKE_INSTALL_PREFIX, CMAKE_MODULE_LINKER_FLAGS, etc.;
// the rockspec is responsible for supplying them via build.variables):
//
//  1. cmake . -D<K>=<V> ...        (configure)
//  2. cmake --build .              (build)
//  3. cmake --install . --prefix destDir
//
// The working directory is srcDir. Builds happen in-tree, matching
// upstream's `-H. -Bbuild.luarocks` shape closely enough for our purposes
// (upstream uses `build.luarocks` as a subdir; we let cmake default to
// in-tree which gives the same outputs on the install step).
//
// The install --prefix argument is the one piece of plumbing we DO inject:
// the facade needs the rock's output rooted at destDir, and cmake's
// --install --prefix is the cleanest way to achieve that without
// post-hoc moving. This matches the spirit of upstream which expects
// CMAKE_INSTALL_PREFIX to be set to $(PREFIX); --prefix on --install
// overrides it.
func runCMake(ctx context.Context, spec *rocks.Rockspec, srcDir, destDir string, cfg rocks.Config) error {
	env := buildEnv(cfg)

	keys := make([]string, 0, len(spec.Build.Variables))
	for k := range spec.Build.Variables {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	configure := make([]string, 0, 1+len(keys))
	configure = append(configure, ".")

	for _, k := range keys {
		configure = append(configure, "-D"+k+"="+spec.Build.Variables[k])
	}

	err := runCmd(ctx, "cmake", configure, srcDir, env)
	if err != nil {
		return err
	}

	err = runCmd(ctx, "cmake", []string{"--build", "."}, srcDir, env)
	if err != nil {
		return err
	}

	return runCmd(ctx, "cmake", []string{"--install", ".", "--prefix", destDir}, srcDir, env)
}
