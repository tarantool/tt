// Package gobuild is the go build backend: go build of a Go program, into
// the directory of the programs of the build (build.Dirs.Programs) under the
// name of the program. Its builds make programs only, and the files of the
// component are not laid out unless its include names them: Go sources are
// no files of a package, and the fixtures of their tests are not either.
//
// The target platform is that of the build, GOOS and GOARCH, and cgo is off
// unless the block turns it on: a program without cgo is static and runs
// whatever the glibc of the machine it is installed on.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package gobuild

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// Name is the name of the go backend.
const Name = "go"

// goTool is the program the backend runs.
const goTool = "go"

// darwinOS is the GOOS spelling of macOS.
const darwinOS = "darwin"

// defaultPackage is the package a go build builds when its block names
// none: the one in its working directory.
const defaultPackage = "."

// staticTags make a cgo program static in practice and not only in its
// link: net and os/user otherwise reach for the NSS modules of glibc, which
// are loaded at run time whatever the linker was told.
var staticTags = []string{"netgo", "osusergo"}

// staticLdflags link a cgo program statically through the external linker,
// which is what links the C part.
var staticLdflags = []string{"-linkmode", "external", "-extldflags", "-static"}

var (
	// ErrStaticDarwin reports a static program for darwin: macOS has no
	// static libSystem to link against, so a static program cannot be made
	// there.
	ErrStaticDarwin = errors.New("a static program cannot be built for darwin")
	// errUnquotableFlag reports a linker flag go build cannot be given.
	errUnquotableFlag = errors.New(
		"a linker flag with a space and both kinds of quotes cannot be passed to go build")
)

// programNameRe is the shape of the name of a program: a plain file name,
// which tt instance finds in .rocks/bin/ by the binary of an instance
// description given without a slash.
var programNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]*$`)

// Backend returns the go backend.
func Backend() build.Backend {
	return build.Backend{
		Name:           Name,
		Produces:       []build.Result{build.Program},
		Config:         func() any { return new(Config) },
		New:            build.Typed(newBuild),
		Incremental:    false,
		OutputFrom:     build.OutputNone,
		Exclude:        nil,
		NoSourceLayout: true,
		Hook:           false,
	}
}

// Config are the fields of a go build block.
type Config struct {
	// Package is the Go package built, "." by default.
	Package string `toml:"package"`
	// Name is the name of the program; the name of the component by
	// default.
	Name string `toml:"name"`
	// Tags are the build tags.
	Tags []string `toml:"tags"`
	// Ldflags are the flags of the linker, one per entry, after tt's own.
	Ldflags []string `toml:"ldflags"`
	// Cgo allows cgo; CGO_ENABLED=0 otherwise.
	Cgo bool `toml:"cgo"`
}

// newBuild checks a go build block of the component.
func newBuild(config *Config, component build.Component, common build.Common) (
	build.Build, error,
) {
	if config.Name != "" && !programNameRe.MatchString(config.Name) {
		return nil, build.FieldErrorf("name", "%q is not a file name", config.Name)
	}

	for _, tag := range config.Tags {
		if tag == "" || strings.ContainsAny(tag, ", \t\n") {
			return nil, build.FieldErrorf("tags", "%q is not a build tag", tag)
		}
	}

	if slices.Contains(config.Ldflags, "") {
		return nil, build.FieldErrorf("ldflags", "must not contain an empty entry")
	}

	program := config.Name
	if program == "" {
		program = component.Name
	}

	return goBuild{config: *config, program: program, static: common.Static}, nil
}

// goBuild is a checked go build block.
type goBuild struct {
	config  Config
	program string
	static  bool
}

// Produces says the build makes a program.
func (b goBuild) Produces() []build.Result {
	return []build.Result{build.Program}
}

// Programs names the one program the build makes.
func (b goBuild) Programs() ([]string, bool) {
	return []string{b.program}, true
}

// Preflight checks that a static program is not asked for darwin and that go
// is in the PATH of the build.
func (b goBuild) Preflight(_ context.Context, c *build.Context) error {
	if b.static && c.Target.OS == darwinOS {
		return fmt.Errorf("%w: static = true needs a linux build", ErrStaticDarwin)
	}

	_, err := c.LookPath(goTool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH",
			build.ErrNoTool, Name, goTool)
	}

	return nil
}

// Run builds the program in the working directory as
// go build -trimpath -o <programs>/<name> <package>, for the target of the
// build.
func (b goBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err != nil {
		return err
	}

	args, err := goArgs(b.config, filepath.Join(c.Dirs.Programs, b.program), b.static)
	if err != nil {
		return err
	}

	cmd := c.Command(ctx, goTool, args...)

	cmd.Env = append(cmd.Env, targetEnv(b.config, c.Target)...)

	return c.Run(cmd)
}

// targetEnv is what selects the target of go build. It comes after the env
// of the block, as the TT_ variables do, so that the block does not build
// for another platform than the one the build checks the program against;
// CC and the rest of it still reach the compiler.
func targetEnv(config Config, target build.Target) []string {
	cgo := "0"
	if config.Cgo {
		cgo = "1"
	}

	return []string{"GOOS=" + target.OS, "GOARCH=" + target.Arch, "CGO_ENABLED=" + cgo}
}

// goArgs builds the go build argv of the program output.
//
// Every entry of ldflags is one argument of the linker, as every entry of
// args is one of a shell command: "-X", "main.version=1.0" is two. go build
// takes them as one -ldflags string it splits on spaces, so an entry with a
// space or a quote is quoted for it.
//
// A static cgo program gets its tags and linker flags first, so that the
// block's own come after them and may override them; the build checks the
// program it gets whatever the flags were.
func goArgs(config Config, output string, static bool) ([]string, error) {
	args := []string{"build", "-trimpath", "-o", output}

	var tags, ldflags []string

	if config.Cgo && static {
		tags = append(tags, staticTags...)
		ldflags = append(ldflags, staticLdflags...)
	}

	tags = append(tags, config.Tags...)
	ldflags = append(ldflags, config.Ldflags...)

	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}

	if len(ldflags) > 0 {
		quoted, err := QuoteFlags(ldflags)
		if err != nil {
			return nil, err
		}

		args = append(args, "-ldflags", quoted)
	}

	pkg := config.Package
	if pkg == "" {
		pkg = defaultPackage
	}

	return append(args, pkg), nil
}

// QuoteFlags joins flags into the string go build splits back into them: on
// spaces, with single or double quotes around a field that has a space, and
// no escapes. A flag with a space and both kinds of quotes cannot be passed.
func QuoteFlags(flags []string) (string, error) {
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
