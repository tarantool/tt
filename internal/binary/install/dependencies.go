package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/v3/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	mageExec       = "mage"
	gccExec        = "gcc"
	gxxExec        = "g++"
	perlExec       = "perl"
	patchExec      = "patch"
	autoreconfExec = "autoreconf"
	automakeExec   = "automake"
	libtoolizeExec = "libtoolize"
	// opensslName is the program of OpenSSL macOS has, and the package of
	// Arch Linux.
	opensslName = "openssl"
)

// errUnknownOS is wrapped by the error of a build of Tarantool on an OS tt
// does not know the packages of, whose dependencies it cannot check, when
// the build without the check is not confirmed.
var errUnknownOS = errors.New("cannot check the build dependencies on an unknown OS")

type missingPackagesError string

func (err missingPackagesError) Error() string {
	return string(err)
}

// Package is a struct containing sys and install name of the package.
type Package struct {
	// sysName is a string containing system name of package.
	sysName string
	// installName is a string containing install name of package.
	installName string
}

var (
	// gentooPackageDB is the database of the packages Portage has installed,
	// the vdb: a directory <category>/<name>-<version> for each of them.
	gentooPackageDB = "/var/db/pkg"

	// runsAsRoot reports whether tt runs as root, who installs packages
	// without sudo.
	runsAsRoot = func() bool { return os.Geteuid() == 0 }
)

// linuxBuildPrograms are the programs a build of Tarantool from source runs
// on Linux, with the package most families have each in, in the order a
// missing one is told; static marks those only the static build, the one tt
// makes without --dynamic, runs:
//   - cmake configures the build and make runs it: that of Tarantool and, in
//     the static build, those of the libraries it builds;
//   - tt clones the source with git, and the build updates the submodules
//     with it;
//   - Tarantool and the libraries have C and C++ sources;
//   - the bundled libunwind, which the backtraces tt enables
//     (-DENABLE_BACKTRACE=ON) use in both builds, has no configure in Git:
//     autoreconf -i of autoconf makes it, and runs automake and libtoolize
//     of libtool;
//   - the static build downloads OpenSSL, ICU, readline, ncurses and zlib
//     and builds them: the Configure of OpenSSL is a perl script, and patch
//     applies the patches of Tarantool to ICU and readline.
//
// The dynamic build links OpenSSL, readline with ncurses, ICU and zlib of the
// system instead, and needs their headers: the packages of a family have
// them (linuxBuildPackages).
var linuxBuildPrograms = []struct {
	name, pkg string
	static    bool
}{
	{cmakeExec, cmakeExec, false},
	{gitExec, gitExec, false},
	{makeExec, makeExec, false},
	{gccExec, gccExec, false},
	{gxxExec, gxxExec, false},
	{autoreconfExec, "autoconf", false},
	{automakeExec, automakeExec, false},
	{libtoolizeExec, "libtool", false},
	{patchExec, patchExec, true},
	{perlExec, perlExec, true},
}

// linuxBuildPackages are the packages of a Linux family that have what a
// build of Tarantool from source needs (linuxBuildPrograms tells what for).
type linuxBuildPackages struct {
	// programs are the packages of the programs the family has elsewhere
	// than linuxBuildPrograms says, by program. An empty one is a program
	// the family does not look for: one in all its systems, with a package
	// that is looked for instead, in common or static.
	programs map[string]string
	// common are the packages with no program to look for that both builds
	// need, static those only the static build needs, and dynamic those
	// only the dynamic one needs.
	common, static, dynamic []string
}

// linuxTarantoolPackages are the packages of the build of Tarantool from
// source of each Linux family tt knows.
var linuxTarantoolPackages = map[osFamily]linuxBuildPackages{
	osFamilyDebian: {
		// build-essential has gcc, g++, make, patch and the headers of libc.
		// perl-base, in every Debian, has /usr/bin/perl without the modules
		// the Configure of OpenSSL uses, such as IPC::Cmd: perl has them.
		programs: map[string]string{
			gccExec: "build-essential", gxxExec: "build-essential", perlExec: "",
		},
		// The configure scripts run sed and the tools of coreutils; both
		// are in the prerequisites of the static build of Tarantool.
		common: []string{"coreutils", "sed"}, // spell-checker:disable-line
		static: []string{perlExec},
		dynamic: []string{
			"libssl-dev", "libreadline-dev", "libncurses-dev", "libicu-dev", "zlib1g-dev",
		},
	},
	osFamilyRedHat: {
		// perl-interpreter has /usr/bin/perl without the modules the
		// Configure of OpenSSL uses: perl has them.
		programs: map[string]string{gxxExec: "gcc-c++", perlExec: ""},
		common:   nil,
		// The static build links libstdc++ statically, -static-libstdc++.
		static: []string{"libstdc++-static", perlExec},
		dynamic: []string{
			"openssl-devel", "readline-devel", "ncurses-devel", "libicu-devel", "zlib-devel",
		},
	},
	// The packages of Gentoo have their headers, and gcc has libstdc++.a.
	// emerge takes category/name atoms.
	osFamilyGentoo: {
		programs: map[string]string{
			cmakeExec: "dev-build/cmake", gitExec: "dev-vcs/git", makeExec: "dev-build/make",
			gccExec: "sys-devel/gcc", gxxExec: "sys-devel/gcc",
			autoreconfExec: "dev-build/autoconf", automakeExec: "dev-build/automake",
			libtoolizeExec: "dev-build/libtool", patchExec: "sys-devel/patch",
			perlExec: "dev-lang/perl",
		},
		common: nil,
		static: nil,
		dynamic: []string{
			"dev-libs/openssl", "sys-libs/readline", "sys-libs/ncurses", "dev-libs/icu",
			"sys-libs/zlib",
		},
	},
	// The packages of Arch Linux have their headers, and gcc has g++ and
	// libstdc++.a. base-devel has all of them but cmake and git.
	osFamilyArch: {
		programs: map[string]string{gxxExec: gccExec},
		common:   nil,
		static:   nil,
		dynamic:  []string{opensslName, "readline", "ncurses", "icu", "zlib"},
	},
	// g++ of Alpine Linux brings musl-dev, the headers of libc, and
	// libstdc++-dev, which has libstdc++.a.
	osFamilyAlpine: {
		programs: nil,
		// The headers of Linux, which musl-dev has not: libev and libeio of
		// Tarantool include them, as OpenSSL does.
		common:  []string{"linux-headers"},
		static:  nil,
		dynamic: []string{"openssl-dev", "readline-dev", "ncurses-dev", "icu-dev", "zlib-dev"},
	},
}

// dependencies returns the programs, with their packages, and the packages
// with no program to look for that a build of Tarantool from source needs on
// a system with these packages: the dynamic build of --dynamic, or the
// static one.
func (packages linuxBuildPackages) dependencies(dynamic bool) ([]Package, []string) {
	programs := []Package{}

	for _, program := range linuxBuildPrograms {
		if program.static && dynamic {
			continue
		}

		pkg, other := packages.programs[program.name]
		if !other {
			pkg = program.pkg
		}

		if pkg != "" {
			programs = append(programs, Package{program.name, pkg})
		}
	}

	others := slices.Clone(packages.common)
	if dynamic {
		return programs, append(others, packages.dynamic...)
	}

	return programs, append(others, packages.static...)
}

// isProgramInstalled checks if program is installed.
func isProgramInstalled(program string) bool {
	_, err := exec.LookPath(program)

	return err == nil
}

// isPackageInstalledWith reports whether the command that asks the package
// manager for a package succeeds.
func isPackageInstalledWith(name string, args ...string) bool {
	return exec.CommandContext(context.Background(), name, args...).Run() == nil
}

// isPackageInstalled checks if package is installed on a system of family.
func isPackageInstalled(family osFamily, packageName string) bool {
	switch family {
	case osFamilyDebian:
		return isPackageInstalledWith("dpkg", "-L", packageName)
	case osFamilyRedHat:
		// --whatprovides finds a package by a name another one provides
		// too: zlib-ng-compat-devel provides zlib-devel on Fedora 40 and
		// newer.
		return isPackageInstalledWith("rpm", "-q", "--whatprovides", packageName)
	case osFamilyGentoo:
		return isGentooPackageInstalled(packageName)
	case osFamilyArch:
		return isPackageInstalledWith("pacman", "-Q", packageName)
	case osFamilyAlpine:
		return isPackageInstalledWith("apk", "info", "-e", packageName)
	case osFamilyDarwin:
		packageList, _ := binary.RunCommandAndGetOutput("brew", "list")

		return strings.Contains(packageList, packageName)
	case osFamilyUnknown:
	}

	return false
}

// isGentooPackageInstalled reports whether Portage has installed a version
// of the package atom names, category/name: whether the package database has
// a directory <name>-<version> in that of the category. It is what portageq
// has_version tells, without starting Python. A version begins with a digit,
// which tells it from the rest of the name of another package, as automake
// of automake-wrapper.
func isGentooPackageInstalled(atom string) bool {
	category, name, found := strings.Cut(atom, "/")
	if !found {
		return false
	}

	entries, err := os.ReadDir(filepath.Join(gentooPackageDB, category))
	if err != nil {
		return false
	}

	for _, entry := range entries {
		version, found := strings.CutPrefix(entry.Name(), name+"-")
		if found && entry.IsDir() && version != "" && version[0] >= '0' && version[0] <= '9' {
			return true
		}
	}

	return false
}

// redHatPackageManager is the package manager of a Red Hat system: dnf, or
// yum where there is no dnf, CentOS 7 say.
func redHatPackageManager() string {
	if isProgramInstalled("dnf") {
		return "dnf"
	}

	return "yum"
}

// programDependenciesInstalled checks if the dependencies of a build of
// program from source, as installCtx asks for it, are installed.
func programDependenciesInstalled(installCtx InstallCtx, program binary.Program) error {
	return dependenciesInstalledOn(installCtx, program, detectOsFamily())
}

// tarantoolDependencies returns the programs, with their packages, and the
// packages with no program to look for that a build of Tarantool from source
// needs on a system of family: the dynamic build of --dynamic, or the static
// one. It is false for a family tt does not know the packages of.
func tarantoolDependencies(family osFamily, dynamic bool) ([]Package, []string, bool) {
	if family == osFamilyDarwin {
		return []Package{
			{cmakeExec, cmakeExec},
			{gitExec, gitExec},
			{makeExec, makeExec},
			{"clang", "clang"},
			{opensslName, opensslName},
		}, nil, true
	}

	packages, known := linuxTarantoolPackages[family]
	if !known {
		return nil, nil, false
	}

	programs, others := packages.dependencies(dynamic)

	return programs, others, true
}

// dependenciesInstalledOn checks if the dependencies of a build of program
// from source, as installCtx asks for it, are installed on a system of
// family.
func dependenciesInstalledOn(installCtx InstallCtx, program binary.Program,
	family osFamily,
) error {
	programs := []Package{}
	packages := []string{}

	switch program {
	case binary.ProgramTt:
		programs = []Package{{mageExec, mageExec}, {gitExec, gitExec}}
	case binary.ProgramCe:
		var known bool

		programs, packages, known = tarantoolDependencies(family, installCtx.Dynamic)
		if !known {
			return confirmUncheckedBuild(installCtx)
		}
	case binary.ProgramUnknown, binary.ProgramEe, binary.ProgramTtEe,
		binary.ProgramTcm, binary.ProgramSDK:
		// These programs have no source-build dependency checks.
	}

	missingPack := []string{}
	// Programs that are installed from source.
	missingPackSrc := []string{}

	for _, program := range programs {
		if !isProgramInstalled(program.sysName) {
			// Mage is installed from source instead of package manager.
			if program.sysName == mageExec {
				missingPackSrc = appendMissing(missingPackSrc, program.installName)
			} else {
				// One package may have two programs: build-essential.
				missingPack = appendMissing(missingPack, program.installName)
			}
		}
	}

	for _, packageName := range packages {
		if !isPackageInstalled(family, packageName) {
			missingPack = appendMissing(missingPack, packageName)
		}
	}

	if len(missingPack) == 0 && len(missingPackSrc) == 0 {
		return nil
	}

	log.Error("The operation requires some dependencies.")

	return missingPackagesError(missingPackagesMessage(family, missingPack, missingPackSrc))
}

// confirmUncheckedBuild asks whether to build Tarantool on an OS tt does
// not know the packages of, without checking the dependencies of the build.
// A no aborts the installation. A confirmation that cannot be asked, under
// --no-prompt or off a terminal, fails it at once, with nothing read: the
// error points at --yes, which answers yes, and at --force, which skips the
// check and the question with it.
func confirmUncheckedBuild(installCtx InstallCtx) error {
	confirmed, err := installCtx.confirm("Unknown OS, can't check if dependencies" +
		" are installed.\n" +
		"Proceed without checking?")
	if err != nil {
		return fmt.Errorf("%w: %w, or --force to skip the check", errUnknownOS, err)
	}

	if !confirmed {
		return binary.ErrCmdAbort
	}

	return nil
}

// appendMissing appends name to missing unless it is there already.
func appendMissing(missing []string, name string) []string {
	if slices.Contains(missing, name) {
		return missing
	}

	return append(missing, name)
}

// missingPackagesMessage says what is missing on a system of family and the
// commands that install it: missing are the packages of the package manager,
// fromSource the programs installed from source.
func missingPackagesMessage(family osFamily, missing, fromSource []string) string {
	var message strings.Builder

	all := strings.Join(append(slices.Clone(missing), fromSource...), " ")

	message.WriteString("Missing packages: " + all + "\n")

	installer := ""

	switch family {
	case osFamilyDarwin:
		message.WriteString("You can install them by running commands:\nbrew install " +
			all + "\n")
	case osFamilyRedHat:
		installer = redHatPackageManager() + " install "
	case osFamilyDebian:
		installer = "apt install "
	case osFamilyGentoo:
		// --noreplace leaves alone a package installed already.
		installer = "emerge --ask --noreplace "
	case osFamilyArch:
		// --needed leaves alone a package installed already.
		installer = "pacman -S --needed "
	case osFamilyAlpine:
		installer = "apk add "
	case osFamilyUnknown:
	}

	if installer != "" {
		if !runsAsRoot() {
			installer = "sudo " + installer
		}

		message.WriteString("You can install them by running command:\n")

		if len(missing) != 0 {
			message.WriteString(" " + installer + strings.Join(missing, " ") + "\n")
		}

		if len(fromSource) != 0 {
			message.WriteString("install from sources: " + strings.Join(fromSource, " ") + "\n")
		}
	}

	message.WriteString("Use --force if you already have those packages installed")

	return message.String()
}
