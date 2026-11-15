package cc

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// This file runs pkg-config for the packages of pkg_config: the flags it
// gives for them go to the compiles and to the link of the build.

// pkgConfigName is the pkg-config a build runs when PKG_CONFIG of the
// environment tt runs in names none.
const pkgConfigName = "pkg-config"

// pkgConfigEnv are the variables of the environment of the build pkg-config
// reads to find the packages and their flags, which are of the keys of the
// objects and of the link of a build that runs it (cache.go): pkg-config
// runs with the environment of the build, so env of the block sets them.
var pkgConfigEnv = []string{"PKG_CONFIG_PATH", "PKG_CONFIG_LIBDIR", "PKG_CONFIG_SYSROOT_DIR"}

// pkgPackage is an entry of pkg_config, of the build or of its overlay of
// the target, with the path of its field.
type pkgPackage struct {
	field string
	name  string
}

// pkgPackages are the entries of pkg_config of config for goos, then those
// of its overlay, in their order, each with its field.
func pkgPackages(config Config, goos string) []pkgPackage {
	packages := make([]pkgPackage, 0, len(config.PkgConfig))

	for index, name := range config.PkgConfig {
		packages = append(packages, pkgPackage{fmt.Sprintf("pkg_config[%d]", index), name})
	}

	for index, name := range config.Platforms[goos].PkgConfig {
		packages = append(packages,
			pkgPackage{fmt.Sprintf("platforms.%s.pkg_config[%d]", goos, index), name})
	}

	return packages
}

// pkgNames are the entries of packages, as pkg-config is given them.
func pkgNames(packages []pkgPackage) []string {
	names := make([]string, 0, len(packages))

	for _, pkg := range packages {
		names = append(names, pkg.name)
	}

	return names
}

// pkgFlags are what pkg-config gives for the packages of a build, split
// into words as a shell splits them.
type pkgFlags struct {
	// cflags are of pkg-config --cflags, of every compile.
	cflags []string
	// libs are of pkg-config --libs, of the link.
	libs []string
}

// checkPkgConfig checks that the pkg-config of tools is in the PATH of the
// build of c and knows each of packages, asking it about each on its own,
// so that the error names the entry: the check of a Preflight, which runs
// before any component is built.
func checkPkgConfig(ctx context.Context, c *build.Context, tools toolchain,
	packages []pkgPackage,
) error {
	if len(packages) == 0 {
		return nil
	}

	_, err := c.LookPath(tools.pkgConfig[0])
	if err != nil {
		return fmt.Errorf("%w: pkg_config lists %s, which the build asks %q about, and it is "+
			"not in PATH; PKG_CONFIG of the environment tt runs in names another",
			build.ErrNoTool, quotedList(pkgNames(packages)), tools.pkgConfig[0])
	}

	for _, pkg := range packages {
		_, err := runPkgConfig(ctx, c, tools, []string{"--exists", "--print-errors"},
			[]string{pkg.name})
		if err != nil {
			return build.FieldErrorf(pkg.field, "%q: %v", pkg.name, err)
		}
	}

	return nil
}

// pkgConfigFlags runs the pkg-config of tools for packages, --cflags and
// --libs, each with --static for a static build, and returns the flags it
// gives; none without packages.
func pkgConfigFlags(ctx context.Context, c *build.Context, tools toolchain,
	packages []pkgPackage, static bool,
) (pkgFlags, error) {
	flags := pkgFlags{cflags: nil, libs: nil}

	if len(packages) == 0 {
		return flags, nil
	}

	for _, query := range []struct {
		flag  string
		words *[]string
	}{{"--cflags", &flags.cflags}, {"--libs", &flags.libs}} {
		options := []string{query.flag}
		if static {
			options = append(options, "--static")
		}

		printed, err := runPkgConfig(ctx, c, tools, options, pkgNames(packages))
		if err != nil {
			return pkgFlags{}, fmt.Errorf("pkg_config %s: %w", quotedList(pkgNames(packages)),
				err)
		}

		words, err := splitWords(printed)
		if err != nil {
			return pkgFlags{}, fmt.Errorf("pkg_config %s: splitting %q: %w",
				quotedList(pkgNames(packages)), printed, err)
		}

		*query.words = words
	}

	return flags, nil
}

// runPkgConfig runs the pkg-config of tools with options for packages, each
// an argument as pkg_config has it, in the environment of the build of c,
// and returns what it printed. It runs the command itself, as compile does,
// so that what it prints on stderr is in the error.
func runPkgConfig(ctx context.Context, c *build.Context, tools toolchain,
	options, packages []string,
) (string, error) {
	argv := concat(concat(tools.pkgConfig, options), packages)
	cmd := c.Command(ctx, argv[0], argv[1:]...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logger(c).DebugContext(ctx, "running "+commandLine(cmd.Args), "dir", cmd.Dir)

	err := cmd.Run()
	if err != nil {
		printed := strings.TrimSpace(stderr.String())
		if printed == "" {
			return "", fmt.Errorf("%s: %w", commandLine(cmd.Args), err)
		}

		return "", fmt.Errorf("%s: %w: %s", commandLine(cmd.Args), err, printed)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// quotedList names each of names quoted, as a sentence lists them.
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))

	for _, name := range names {
		quoted = append(quoted, strconv.Quote(name))
	}

	return andList(quoted)
}

// andList joins names as a sentence lists them: "a", "a and b", "a, b and
// c".
func andList(names []string) string {
	last := len(names) - 1
	if last <= 0 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:last], ", ") + " and " + names[last]
}
