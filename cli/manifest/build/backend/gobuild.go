package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// goTool is the program the go backend runs.
const goTool = "go"

// defaultGoPackage is the package a go build builds when the manifest names
// none: the one in its working directory.
const defaultGoPackage = "."

// staticGoTags make a cgo program static in practice and not only in its
// link: net and os/user otherwise reach for the NSS modules of glibc, which
// are loaded at run time whatever the linker was told.
var staticGoTags = []string{"netgo", "osusergo"}

// staticGoLdflags link a cgo program statically through the external
// linker, which is what links the C part.
var staticGoLdflags = []string{"-linkmode", "external", "-extldflags", "-static"}

// goBackend builds a Go program with go build into TT_OUTPUT_DIR.
//
// The target platform comes from the build platform, GOOS and GOARCH, and
// cgo is off unless the manifest turns it on: a program without cgo is
// static and runs whatever the glibc of the machine it is installed on.
type goBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// Run builds the program of build in cwd as
// go build -trimpath -o $TT_OUTPUT_DIR/<name> <package>.
func (g goBackend) Run(ctx context.Context, build manifest.Build, cwd string, env Env) error {
	err := requireAbsPaths(cwd, env.OutputDir)
	if err != nil {
		return err
	}

	err = Preflight(build, env)
	if err != nil {
		return err
	}

	args, err := goArgs(build, env)
	if err != nil {
		return err
	}

	err = os.MkdirAll(env.OutputDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create output directory %q: %w", env.OutputDir, err)
	}

	return runEnviron(ctx, cwd, goEnviron(build, env), g.showOutput, goTool, args...)
}

// goEnviron is the component-build contract plus what selects the target of
// go build. They come after [build].env, as the TT_ variables do, so that
// the env of the manifest does not build for another platform than the one
// the build checks the program against; CC and the rest of it still reach
// the compiler.
func goEnviron(build manifest.Build, env Env) []string {
	cgo := "0"
	if build.CgoValue() {
		cgo = "1"
	}

	return append(env.environ(),
		"GOOS="+env.platformOS(),
		"GOARCH="+env.platformArch(),
		"CGO_ENABLED="+cgo,
	)
}

// goArgs builds the go build argv.
//
// Every entry of ldflags is one argument of the linker, as every entry of
// args is one of a shell command: "-X", "main.version=1.0" is two. go build
// takes them as one -ldflags string it splits on spaces, so an entry with a
// space or a quote is quoted for it.
//
// A static cgo program gets its tags and linker flags first, so that the
// manifest's own come after them and may override them; the build checks the
// program it gets whatever the flags were.
func goArgs(build manifest.Build, env Env) ([]string, error) {
	output := filepath.Join(env.OutputDir, build.Executables(env.Component)[0])
	args := []string{"build", "-trimpath", "-o", output}

	var tags, ldflags []string

	if build.CgoValue() && build.StaticValue() {
		tags = append(tags, staticGoTags...)
		ldflags = append(ldflags, staticGoLdflags...)
	}

	tags = append(tags, build.Tags...)
	ldflags = append(ldflags, build.Ldflags...)

	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}

	if len(ldflags) > 0 {
		quoted, err := quoteFlags(ldflags)
		if err != nil {
			return nil, err
		}

		args = append(args, "-ldflags", quoted)
	}

	pkg := build.Package
	if pkg == "" {
		pkg = defaultGoPackage
	}

	return append(args, pkg), nil
}

// quoteFlags joins flags into the string go build splits back into them: on
// spaces, with single or double quotes around a field that has a space, and
// no escapes. A flag with a space and both kinds of quotes cannot be passed.
func quoteFlags(flags []string) (string, error) {
	quoted := make([]string, 0, len(flags))

	for _, flag := range flags {
		switch {
		case !strings.ContainsAny(flag, " \t\n\r'\""):
			quoted = append(quoted, flag)
		case !strings.Contains(flag, "'"):
			quoted = append(quoted, "'"+flag+"'")
		case !strings.Contains(flag, `"`):
			quoted = append(quoted, `"`+flag+`"`)
		default:
			return "", fmt.Errorf("%w: %q", errUnquotableFlag, flag)
		}
	}

	return strings.Join(quoted, " "), nil
}
