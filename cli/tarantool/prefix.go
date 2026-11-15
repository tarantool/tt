package tarantool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// PrefixEnv names the environment variable that sets the install prefix of
// Tarantool, the directory whose include/tarantool holds its C headers.
const PrefixEnv = "TT_CLI_TARANTOOL_PREFIX"

// DefaultPrefix is the prefix of the Tarantool of the system packages, where
// a tarantool-dev package puts the headers: the last place ResolvePrefix
// looks for them, and the prefix it returns, for want of a better one, when
// the prefix the tarantool was built for does not exist.
const DefaultPrefix = "/usr"

const (
	// moduleHeader is the header of the module API of Tarantool: an include
	// directory without it holds no headers of Tarantool.
	moduleHeader = "module.h"
	// bannerLines is how many lines of tarantool --version ResolvePrefix
	// needs: the version, the target and the build options.
	bannerLines = 3
	// bannerOptionsLine is the line of the build options in the banner.
	bannerOptionsLine = 2
	// versionParts are the parts of a version compared: major.minor.patch.
	versionParts = 3
)

var (
	// ErrBannerShort reports a tarantool --version that does not print the
	// build options, the third line.
	ErrBannerShort = errors.New("failed to get prefix path: expected more data")
	// ErrBannerNoPrefix reports build options of tarantool --version that do
	// not name CMAKE_INSTALL_PREFIX.
	ErrBannerNoPrefix = errors.New("failed to get prefix path: regexp does not match")
	// ErrNoExecutable reports a prefix to be taken from the banner of a
	// tarantool when there is none.
	ErrNoExecutable = errors.New("failed to get tarantool version: no tarantool executable")
)

var (
	// installPrefixRe finds the install prefix in the build options.
	installPrefixRe = regexp.MustCompile(`(?:^|\s)-DCMAKE_INSTALL_PREFIX=(/\S*)`)
	// versionRe finds major.minor.patch in a version string.
	versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
	// headerVersionRe finds the PACKAGE_VERSION_MAJOR, _MINOR and _PATCH
	// definitions of module.h.
	headerVersionRe = regexp.MustCompile(
		`(?m)^#define\s+PACKAGE_VERSION_(MAJOR|MINOR|PATCH)\s+(\d+)\s*$`)
	// headerVersionStrRe finds the PACKAGE_VERSION definition of module.h.
	headerVersionStrRe = regexp.MustCompile(`(?m)^#define\s+PACKAGE_VERSION\s+"([^"]*)"`)
)

// PrefixOrigin tells where ResolvePrefix took the prefix from.
type PrefixOrigin int

const (
	// PrefixOriginUnknown is the zero PrefixOrigin; ResolvePrefix never
	// returns it.
	PrefixOriginUnknown PrefixOrigin = iota
	// PrefixOriginProject is the root of the project scope, for the
	// tarantool of the project, .tarantool/bin/tarantool.
	PrefixOriginProject
	// PrefixOriginEnv is TT_CLI_TARANTOOL_PREFIX.
	PrefixOriginEnv
	// PrefixOriginExecutable is a directory of the executable, or the one
	// above it, that holds the headers.
	PrefixOriginExecutable
	// PrefixOriginBuild is CMAKE_INSTALL_PREFIX of tarantool --version,
	// which holds the headers.
	PrefixOriginBuild
	// PrefixOriginDefault is DefaultPrefix, which holds the headers.
	PrefixOriginDefault
	// PrefixOriginGuess is no prefix that holds the headers: the
	// CMAKE_INSTALL_PREFIX of the build when it exists, else DefaultPrefix.
	PrefixOriginGuess
)

// PrefixQuery is what ResolvePrefix resolves the prefix of.
type PrefixQuery struct {
	// Project is the prefix of a tarantool of the project scope, the root
	// of the scope, where tt binary install puts its headers; empty for a
	// tarantool of another origin.
	Project string
	// Env is the value of TT_CLI_TARANTOOL_PREFIX.
	Env string
	// Executable is the tarantool, as it was found: a link of a scope, say.
	Executable string
}

// Prefix is the install prefix of a tarantool ResolvePrefix found.
type Prefix struct {
	// Dir is the prefix. It is never empty: a tarantool whose headers are
	// nowhere gets the prefix of PrefixOriginGuess, which builds what
	// compiles no C.
	Dir string
	// Origin tells where Dir was taken from.
	Origin PrefixOrigin
	// IncludeDir is <Dir>/include/tarantool when module.h is there, and
	// empty otherwise.
	IncludeDir string
	// Tried are the include directories looked into for module.h, in order,
	// IncludeDir the last of them when it is set.
	Tried []string
	// Executable is the tarantool of the query.
	Executable string
	// Version is the version of the executable, from the first line of
	// tarantool --version; empty when it did not tell one.
	Version string
	// HeadersVersion is the version module.h of IncludeDir defines; empty
	// when there are no headers or they define none.
	HeadersVersion string
	// Warnings are diagnostics that do not stop the build, for the caller
	// to surface: headers of another version than the executable.
	Warnings []string
}

// ResolvePrefix returns the install prefix of the tarantool of q, the
// directory whose include/tarantool holds its headers. The first of these
// wins:
//
//  1. The prefix the caller names: q.Project, then q.Env. It is taken
//     whether or not it holds the headers.
//  2. A directory of the executable, its real path first - symbolic links
//     resolved, the headers of this very build - then the path as found -
//     the link of a scope, the active headers of the scope. For each, the
//     directory above the one of the executable, as for <prefix>/bin/
//     tarantool, then the directory of the executable itself, as for an SDK
//     of Tarantool Enterprise or the version directory of tt binary install.
//  3. CMAKE_INSTALL_PREFIX of tarantool --version, the prefix the build was
//     to be installed into, then DefaultPrefix.
//
// From 2 on a prefix is only taken when its include/tarantool has module.h.
// With none of them it is the guess of PrefixOriginGuess, and IncludeDir is
// empty: [Prefix.MissingHeaders] says where it looked.
//
// Headers of another version than the executable are a warning.
//
// An executable that cannot be run, or whose tarantool --version names no
// CMAKE_INSTALL_PREFIX, is an error when the prefix is to be taken from it.
func ResolvePrefix(query PrefixQuery) (Prefix, error) {
	res := Prefix{
		Dir: "", Origin: PrefixOriginUnknown, IncludeDir: "", Tried: nil,
		Executable: query.Executable, Version: "", HeadersVersion: "", Warnings: nil,
	}

	var banner []string

	bannerErr := ErrNoExecutable
	if query.Executable != "" {
		banner, bannerErr = readBanner(query.Executable)
	}

	if bannerErr == nil {
		fields := strings.Fields(banner[0])
		if len(fields) > 0 {
			res.Version = fields[len(fields)-1]
		}
	}

	switch {
	case query.Project != "":
		res.Dir, res.Origin = query.Project, PrefixOriginProject
		lookForHeaders(&res, query.Project)
	case query.Env != "":
		res.Dir, res.Origin = query.Env, PrefixOriginEnv
		lookForHeaders(&res, query.Env)
	default:
		err := searchPrefix(&res, banner, bannerErr)
		if err != nil {
			return Prefix{}, err
		}
	}

	checkHeadersVersion(&res)

	return res, nil
}

// MissingHeaders says, when the prefix has no headers, where they were looked
// for and how to point tt at them; it is empty when IncludeDir is set.
func (p Prefix) MissingHeaders() string {
	if p.IncludeDir != "" {
		return ""
	}

	switch p.Origin {
	case PrefixOriginEnv:
		return fmt.Sprintf("%s=%s has no %s in %s: set it to the install prefix of "+
			"Tarantool, the directory holding include/tarantool", PrefixEnv, p.Dir,
			moduleHeader, strings.Join(p.Tried, ", "))
	case PrefixOriginProject:
		return fmt.Sprintf("the tarantool of the project has no %s in %s: "+
			"tt binary install tarantool installs it with its headers", moduleHeader,
			strings.Join(p.Tried, ", "))
	default:
		return fmt.Sprintf("none found for %s, no %s in %s: set %s to the install prefix "+
			"of Tarantool, the directory holding include/tarantool", p.Executable, moduleHeader,
			strings.Join(p.Tried, ", "), PrefixEnv)
	}
}

// searchPrefix looks for the prefix of the executable of res with banner,
// its tarantool --version, or bannerErr, the error of running it: steps 2
// and 3 of ResolvePrefix.
func searchPrefix(res *Prefix, banner []string, bannerErr error) error {
	found := slices.ContainsFunc(executableDirs(res.Executable), func(dir string) bool {
		return lookForHeaders(res, dir)
	})
	if found {
		res.Origin = PrefixOriginExecutable

		return nil
	}

	if bannerErr != nil {
		return bannerErr
	}

	built, err := buildPrefix(banner)
	if err != nil {
		return err
	}

	switch {
	case lookForHeaders(res, built):
		res.Origin = PrefixOriginBuild
	case lookForHeaders(res, DefaultPrefix):
		res.Origin = PrefixOriginDefault
	default:
		res.Origin = PrefixOriginGuess
		res.Dir = DefaultPrefix

		info, statErr := os.Stat(built)
		if statErr == nil && info.IsDir() {
			res.Dir = built
		}
	}

	return nil
}

// lookForHeaders adds <dir>/include/tarantool to the tried directories of
// res, and takes dir as its prefix when module.h is there, reporting whether
// it is.
func lookForHeaders(res *Prefix, dir string) bool {
	include := filepath.Join(dir, "include", "tarantool")
	if slices.Contains(res.Tried, include) {
		return false
	}

	res.Tried = append(res.Tried, include)

	info, err := os.Stat(filepath.Join(include, moduleHeader))
	if err != nil || info.IsDir() {
		return false
	}

	res.Dir, res.IncludeDir = dir, include

	return true
}

// checkHeadersVersion reads the version of the headers of res, and warns
// when it is not the version of the executable.
func checkHeadersVersion(res *Prefix) {
	if res.IncludeDir == "" {
		return
	}

	header, err := os.ReadFile(filepath.Join(res.IncludeDir, moduleHeader))
	if err != nil {
		return
	}

	var headerParts []string

	res.HeadersVersion, headerParts = headerVersion(string(header))

	exeParts := versionRe.FindStringSubmatch(res.Version)
	if headerParts == nil || exeParts == nil {
		return
	}

	if strings.Join(headerParts, ".") == strings.Join(exeParts[1:], ".") {
		return
	}

	res.Warnings = append(res.Warnings, fmt.Sprintf(
		"the headers of Tarantool in %s are of version %s, the tarantool %s is %s: "+
			"a module built against them may not load; set %s to the prefix of its own",
		res.IncludeDir, res.HeadersVersion, res.Executable, res.Version, PrefixEnv))
}

// readBanner runs tarantool --version of executable and returns its lines.
func readBanner(executable string) ([]string, error) {
	output, err := exec.CommandContext(context.Background(), executable, "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get tarantool version: %w", err)
	}

	return strings.Split(strings.TrimSpace(string(output)), "\n"), nil
}

// buildPrefix returns CMAKE_INSTALL_PREFIX of the build options of banner.
func buildPrefix(banner []string) (string, error) {
	if len(banner) < bannerLines {
		return "", ErrBannerShort
	}

	match := installPrefixRe.FindStringSubmatch(banner[bannerOptionsLine])
	if match == nil {
		return "", ErrBannerNoPrefix
	}

	return match[1], nil
}

// executableDirs are the candidate prefixes of step 2 of ResolvePrefix for
// executable, each once.
func executableDirs(executable string) []string {
	found, err := filepath.Abs(executable)
	if err != nil {
		return nil
	}

	paths := []string{found}

	resolved, err := filepath.EvalSymlinks(found)
	if err == nil {
		paths = []string{resolved, found}
	}

	var dirs []string

	for _, path := range paths {
		dir := filepath.Dir(path)
		for _, candidate := range []string{filepath.Dir(dir), dir} {
			if !slices.Contains(dirs, candidate) {
				dirs = append(dirs, candidate)
			}
		}
	}

	return dirs
}

// headerVersion returns the version module.h defines, PACKAGE_VERSION, and
// its major, minor and patch: PACKAGE_VERSION_MAJOR, _MINOR and _PATCH, else
// those of PACKAGE_VERSION. The parts are nil when it defines none.
func headerVersion(header string) (string, []string) {
	str := ""
	if match := headerVersionStrRe.FindStringSubmatch(header); match != nil {
		str = match[1]
	}

	parts := map[string]string{}
	for _, match := range headerVersionRe.FindAllStringSubmatch(header, -1) {
		parts[match[1]] = match[2]
	}

	if len(parts) == versionParts {
		if str == "" {
			str = parts["MAJOR"] + "." + parts["MINOR"] + "." + parts["PATCH"]
		}

		return str, []string{parts["MAJOR"], parts["MINOR"], parts["PATCH"]}
	}

	if match := versionRe.FindStringSubmatch(str); match != nil {
		return str, match[1:]
	}

	return str, nil
}
