package cc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// This file loads a module just linked into the Tarantool of the build, so
// that a module the loader refuses - a library forgotten in libraries, the
// headers of a Tarantool newer than the one it runs in - fails its build
// rather than its first require.

// loadTimeout is how long the load of a module may take.
const loadTimeout = 30 * time.Second

// The variables of the environment of the load that name the module and the
// symbol, and the lines the script prints: a tarantool that prints neither,
// a script of a test, say, has not loaded the module.
const (
	loadModuleEnv = "TT_CC_LOAD_MODULE"
	loadSymbolEnv = "TT_CC_LOAD_SYMBOL"
	loadedMark    = "tt-cc-load: ok"
	failedMark    = "tt-cc-load: failed"
)

// loadScript loads the module of TT_CC_LOAD_MODULE with package.loadlib, as
// require loads a C module: LuaJIT opens it with dlopen(RTLD_NOW), so every
// symbol it needs is resolved, and for a symbol other than "*" looks the
// symbol up without calling it. It ends with os.exit, as tarantool -e runs
// on otherwise.
const loadScript = `local f, err = package.loadlib(os.getenv('` + loadModuleEnv + `'), ` +
	`os.getenv('` + loadSymbolEnv + `'))
if f then io.stdout:write('` + loadedMark + `\n') io.stdout:flush() os.exit(0) end
io.stdout:write('` + failedMark + `\n', tostring(err), '\n') io.stdout:flush() os.exit(1)`

// errNoLoad reports a module the Tarantool of the build does not load.
var errNoLoad = errors.New("the module does not load")

// loadSymbol is the symbol the load of a module looks up: the entry point of
// a lua-c module required by required, which loadlib finds without calling
// it, and "*" for a module of c, whose functions box.schema.func names.
func loadSymbol(required string) string {
	if required == "" {
		return "*"
	}

	return luaEntry(required)
}

// testLoad loads the module just linked into the tarantool of the build, as
// require and box.schema.func load it, and fails the build with what the
// loader says when it does not load. It loads nothing when the build has no
// tarantool, is for another platform than the host, or the tarantool does
// not run the script, saying so in a debug record: the load is to tell why a
// module would not load, not to fail a build of a tarantool it cannot run.
func (r runner) testLoad(ctx context.Context) error {
	c := r.c
	out := r.built.out
	executable := c.Tarantool.Executable
	log := logger(c)

	if executable == "" {
		log.DebugContext(ctx, out+" is not loaded: the build has no tarantool")

		return nil
	}

	if c.Target.OS != runtime.GOOS || c.Target.Arch != runtime.GOARCH {
		log.DebugContext(ctx, out+" is not loaded: it is not of the host")

		return nil
	}

	loadCtx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()

	cmd := c.Command(loadCtx, executable, "-e", loadScript)

	cmd.Env = append(cmd.Env, loadModuleEnv+"="+out, loadSymbolEnv+"="+loadSymbol(r.required))

	var output bytes.Buffer

	cmd.Stdout = &output
	cmd.Stderr = &output

	log.DebugContext(ctx, "loading "+out, "command", executable+" -e <script>")

	runErr := cmd.Run()
	printed := output.String()

	if ctx.Err() != nil {
		return fmt.Errorf("loading %s: %w", out, ctx.Err())
	}

	_, after, failed := strings.Cut(printed, failedMark+"\n")

	switch {
	case failed:
		return fmt.Errorf("%s: %w into %s: %s", out, errNoLoad, executable,
			strings.TrimSpace(after))
	case strings.Contains(printed, loadedMark):
		return nil
	case loadCtx.Err() != nil:
		warner(c)(fmt.Sprintf("%s is not checked: its load into %s did not end in %s",
			out, executable, loadTimeout))
	default:
		log.DebugContext(ctx, fmt.Sprintf("%s is not loaded: %s did not run the script: %v\n%s",
			out, executable, runErr, printed))
	}

	return nil
}
