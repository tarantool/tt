package build

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// stderrTailLimit caps how many trailing bytes of a failed command's stderr
// are embedded in the returned error.
const stderrTailLimit = 4096

// runCmd shells out to `name args...` with the given working directory and
// extra environment overlay. extraEnv overrides any matching key in
// os.Environ(). Stdout/stderr are captured; on non-zero exit the returned
// error embeds the program name, args, and a tail of stderr.
//
// This is the ONLY entry point in the build/ package that
// constructs an exec.Cmd, and the entire env path goes through extraEnv —
// we never os.Setenv. The context is passed via
// exec.CommandContext so cancellation propagates to the child process
// group.
func runCmd(ctx context.Context, name string, args []string, cwd string, extraEnv map[string]string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name/args are rockspec-derived build tooling, internally controlled
	if cwd != "" {
		cmd.Dir = cwd
	}

	cmd.Env = mergeEnv(os.Environ(), extraEnv)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > stderrTailLimit {
			tail = tail[len(tail)-stderrTailLimit:]
		}

		return fmt.Errorf("build: %s %s: %w (stderr: %s)",
			name, strings.Join(args, " "), err, tail)
	}

	return nil
}

// mergeEnv produces a final environment slice by starting from base
// (typically os.Environ()) and replacing or appending each key in
// overlay. Determinism of order is not guaranteed by exec — but for tests
// we sort overlay keys so assertions over cmd.Env are stable.
func mergeEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		out := make([]string, len(base))
		copy(out, base)

		return out
	}
	// Build an index of base entries by key for in-place override.
	idx := make(map[string]int, len(base))

	out := make([]string, 0, len(base)+len(overlay))

	for i, e := range base {
		k, _, ok := splitEnv(e)
		if !ok {
			out = append(out, e)

			continue
		}

		idx[k] = i

		out = append(out, e)
	}

	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		entry := k + "=" + overlay[k]
		if i, ok := idx[k]; ok {
			out[i] = entry

			continue
		}

		out = append(out, entry)
	}

	return out
}

// splitEnv splits a "KEY=VAL" string. Returns ok=false for entries without
// an "=" (defensive — should not occur in os.Environ output).
func splitEnv(e string) (string, string, bool) {
	before, after, ok := strings.Cut(e, "=")
	if !ok {
		return "", "", false
	}

	return before, after, true
}

// buildEnv produces the canonical {TARANTOOL_DIR, LUA, LUA_INCDIR,
// LUA_LIBDIR, LUA_BINDIR} environment overlay for a subprocess invocation.
// All five vars come from rocks.Config — never from the
// host process env.
//
// Entries with empty values are omitted so the child shell sees them as
// unset rather than as the empty string.
func buildEnv(cfg rocks.Config) map[string]string {
	env := map[string]string{}
	if cfg.Tarantool.Prefix != "" {
		env["TARANTOOL_DIR"] = cfg.Tarantool.Prefix
	}

	if cfg.Tarantool.Executable != "" {
		env["LUA"] = cfg.Tarantool.Executable
	}

	if cfg.Tarantool.IncludeDir != "" {
		env["LUA_INCDIR"] = cfg.Tarantool.IncludeDir
	}

	f := DeriveFlags(cfg)
	if f.LuaLibDir != "" {
		env["LUA_LIBDIR"] = f.LuaLibDir
	}

	if f.LuaBinDir != "" {
		env["LUA_BINDIR"] = f.LuaBinDir
	}

	return env
}
