package install

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

// The os-release files of the distributions, as they ship them, trimmed.
const (
	centos7Release = `NAME="CentOS Linux"
VERSION="7 (Core)"
ID="centos"
ID_LIKE="rhel fedora"
VERSION_ID="7"
PRETTY_NAME="CentOS Linux 7 (Core)"
`
	centosStream9Release = `NAME="CentOS Stream"
VERSION="9"
ID="centos"
ID_LIKE="rhel fedora"
PRETTY_NAME="CentOS Stream 9"
`
	rhel9Release = `NAME="Red Hat Enterprise Linux"
ID="rhel"
ID_LIKE="fedora"
PRETTY_NAME="Red Hat Enterprise Linux 9.4 (Plow)"
`
	rocky9Release = `NAME="Rocky Linux"
ID="rocky"
ID_LIKE="rhel centos fedora"
PRETTY_NAME="Rocky Linux 9.4 (Blue Onyx)"
`
	alma9Release = `NAME="AlmaLinux"
ID="almalinux"
ID_LIKE="rhel centos fedora"
PRETTY_NAME="AlmaLinux 9.4 (Seafoam Ocelot)"
`
	fedoraRelease = `NAME="Fedora Linux"
ID=fedora
PRETTY_NAME="Fedora Linux 40 (Workstation Edition)"
`
	oracleRelease = `NAME="Oracle Linux Server"
ID="ol"
ID_LIKE="fedora"
`
	ubuntuRelease = `PRETTY_NAME="Ubuntu 24.04.1 LTS"
NAME="Ubuntu"
ID=ubuntu
ID_LIKE=debian
`
	debianRelease = `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
ID=debian
`
	mintRelease = `NAME="Linux Mint"
ID=linuxmint
ID_LIKE="ubuntu debian"
`
	archRelease = `NAME="Arch Linux"
PRETTY_NAME="Arch Linux"
ID=arch
`
	manjaroRelease = `NAME="Manjaro Linux"
PRETTY_NAME="Manjaro Linux"
ID=manjaro
ID_LIKE=arch
BUILD_ID=rolling
`
	endeavourRelease = `NAME="EndeavourOS"
PRETTY_NAME="EndeavourOS"
ID="endeavouros"
ID_LIKE="arch"
`
	alpineRelease = `NAME="Alpine Linux"
ID=alpine
VERSION_ID=3.22.6
PRETTY_NAME="Alpine Linux v3.22"
`
	postmarketRelease = `PRETTY_NAME="postmarketOS v25.06"
NAME="postmarketOS"
ID="postmarketos"
ID_LIKE="alpine"
`
	gentooRelease = `NAME='Gentoo'
ID='gentoo'
PRETTY_NAME='Gentoo Linux'
`
	tumbleweedRelease = `NAME="openSUSE Tumbleweed"
ID="opensuse-tumbleweed"
ID_LIKE="opensuse suse"
`
	voidRelease = `NAME="Void"
ID="void"
PRETTY_NAME="Void Linux"
`
)

func TestOSReleaseFamily(t *testing.T) {
	for name, test := range map[string]struct {
		release string
		want    osFamily
	}{
		"CentOS 7":        {centos7Release, osFamilyRedHat},
		"CentOS Stream 9": {centosStream9Release, osFamilyRedHat},
		"RHEL 9":          {rhel9Release, osFamilyRedHat},
		"Rocky Linux 9":   {rocky9Release, osFamilyRedHat},
		"AlmaLinux 9":     {alma9Release, osFamilyRedHat},
		"Fedora 40":       {fedoraRelease, osFamilyRedHat},
		"Oracle Linux":    {oracleRelease, osFamilyRedHat},
		"Ubuntu 24.04":    {ubuntuRelease, osFamilyDebian},
		"Debian 12":       {debianRelease, osFamilyDebian},
		"Linux Mint":      {mintRelease, osFamilyDebian},
		"upper case":      {"ID=CentOS\n", osFamilyRedHat},
		"single quotes":   {"ID='rocky'\n", osFamilyRedHat},
		"Arch Linux":      {archRelease, osFamilyArch},
		"Manjaro":         {manjaroRelease, osFamilyArch},
		"EndeavourOS":     {endeavourRelease, osFamilyArch},
		"Alpine Linux":    {alpineRelease, osFamilyAlpine},
		"postmarketOS":    {postmarketRelease, osFamilyAlpine},
		"Gentoo":          {gentooRelease, osFamilyGentoo},
		"Tumbleweed":      {tumbleweedRelease, osFamilyUnknown},
		"Void Linux":      {voidRelease, osFamilyUnknown},
		"empty":           {"", osFamilyUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "os-release")
			require.NoError(t, os.WriteFile(path, []byte(test.release), 0o644))

			release, err := readOSRelease(path)
			require.NoError(t, err)
			assert.Equal(t, test.want, release.family())
		})
	}

	_, err := readOSRelease(filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// asRoot makes tt run as root, or not, as root says, for the test.
func asRoot(t *testing.T, root bool) {
	t.Helper()

	previous := runsAsRoot

	t.Cleanup(func() { runsAsRoot = previous })

	runsAsRoot = func() bool { return root }
}

// withGentooPackages makes the package database of Portage a directory with
// a directory for each of installed, category/name-version, for the test.
func withGentooPackages(t *testing.T, installed ...string) {
	t.Helper()

	database := t.TempDir()
	for _, entry := range installed {
		require.NoError(t, os.MkdirAll(filepath.Join(database, entry), 0o755))
	}

	previous := gentooPackageDB

	t.Cleanup(func() { gentooPackageDB = previous })

	gentooPackageDB = database
}

// withPrograms makes PATH a directory with a program of each of names that
// exits with 0, for the test, and returns the directory.
func withPrograms(t *testing.T, names ...string) string {
	t.Helper()

	commands := t.TempDir()
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(commands, name),
			[]byte("#!/bin/sh\nexit 0\n"), 0o755))
	}

	t.Setenv("PATH", commands)

	return commands
}

// fakePackageManager writes the package manager name into commands: it
// tells installed from other packages by its last argument, the name of
// the package, and appends its arguments to the file it returns.
func fakePackageManager(t *testing.T, commands, name string, installed ...string) string {
	t.Helper()

	calls := filepath.Join(t.TempDir(), name+".calls")

	var script strings.Builder

	script.WriteString("#!/bin/sh\necho \"$*\" >> '" + calls + "'\n" +
		"for last; do :; done\ncase \"$last\" in\n")

	for _, pkg := range installed {
		script.WriteString("'" + pkg + "') exit 0 ;;\n")
	}

	script.WriteString("esac\nexit 1\n")

	require.NoError(t, os.WriteFile(filepath.Join(commands, name), []byte(script.String()),
		0o755))

	return calls
}

// buildPrograms are the programs the static build of Tarantool runs on Linux.
var buildPrograms = []string{
	"cmake", "git", "make", "gcc", "g++", "autoreconf", "automake", "libtoolize", "patch",
	"perl",
}

func TestDependenciesMessage(t *testing.T) {
	for _, test := range []struct {
		name    string
		family  osFamily
		dynamic bool
		manager string
		root    bool
		want    string
	}{
		{
			name: "CentOS 7", family: osFamilyRedHat,
			want: "Missing packages: cmake git make gcc gcc-c++ autoconf automake libtool patch " +
				"libstdc++-static perl\n" +
				"You can install them by running command:\n" +
				" sudo yum install cmake git make gcc gcc-c++ autoconf automake libtool patch " +
				"libstdc++-static perl\n",
		},
		{
			name: "dnf", family: osFamilyRedHat, manager: "dnf",
			want: " sudo dnf install cmake git make gcc gcc-c++ autoconf automake libtool patch " +
				"libstdc++-static perl\n",
		},
		{
			name: "Red Hat --dynamic", family: osFamilyRedHat, dynamic: true, manager: "dnf",
			want: " sudo dnf install cmake git make gcc gcc-c++ autoconf automake libtool " +
				"openssl-devel readline-devel ncurses-devel libicu-devel zlib-devel\n",
		},
		{
			name: "Debian", family: osFamilyDebian,
			want: "Missing packages: cmake git make build-essential autoconf automake libtool " +
				"patch coreutils sed perl\n" +
				"You can install them by running command:\n" +
				" sudo apt install cmake git make build-essential autoconf automake libtool " +
				"patch coreutils sed perl\n",
		},
		{
			name: "Debian --dynamic", family: osFamilyDebian, dynamic: true,
			want: " sudo apt install cmake git make build-essential autoconf automake libtool " +
				"coreutils sed libssl-dev libreadline-dev libncurses-dev libicu-dev zlib1g-dev\n",
		},
		{
			name: "Gentoo", family: osFamilyGentoo,
			want: "Missing packages: dev-build/cmake dev-vcs/git dev-build/make sys-devel/gcc " +
				"dev-build/autoconf dev-build/automake dev-build/libtool sys-devel/patch " +
				"dev-lang/perl\n" +
				"You can install them by running command:\n" +
				" sudo emerge --ask --noreplace dev-build/cmake dev-vcs/git dev-build/make " +
				"sys-devel/gcc dev-build/autoconf dev-build/automake dev-build/libtool " +
				"sys-devel/patch dev-lang/perl\n",
		},
		{
			name: "Gentoo --dynamic", family: osFamilyGentoo, dynamic: true,
			want: "dev-build/libtool dev-libs/openssl sys-libs/readline sys-libs/ncurses " +
				"dev-libs/icu sys-libs/zlib\n",
		},
		{
			name: "Arch Linux", family: osFamilyArch,
			want: "Missing packages: cmake git make gcc autoconf automake libtool patch perl\n" +
				"You can install them by running command:\n" +
				" sudo pacman -S --needed cmake git make gcc autoconf automake libtool patch " +
				"perl\n",
		},
		{
			name: "Arch Linux --dynamic", family: osFamilyArch, dynamic: true,
			want: " sudo pacman -S --needed cmake git make gcc autoconf automake libtool " +
				"openssl readline ncurses icu zlib\n",
		},
		{
			name: "Alpine Linux", family: osFamilyAlpine,
			want: "Missing packages: cmake git make gcc g++ autoconf automake libtool patch " +
				"perl linux-headers\n" +
				"You can install them by running command:\n" +
				" sudo apk add cmake git make gcc g++ autoconf automake libtool patch perl " +
				"linux-headers\n",
		},
		{
			name: "Alpine Linux --dynamic", family: osFamilyAlpine, dynamic: true,
			want: " sudo apk add cmake git make gcc g++ autoconf automake libtool linux-headers " +
				"openssl-dev readline-dev ncurses-dev icu-dev zlib-dev\n",
		},
		{
			name: "root", family: osFamilyAlpine, root: true,
			want: "You can install them by running command:\n apk add cmake git",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Neither the programs nor the package managers are there.
			commands := withPrograms(t)
			if test.manager != "" {
				require.NoError(t, os.WriteFile(filepath.Join(commands, test.manager),
					[]byte("#!/bin/sh\nexit 1\n"), 0o755))
			}

			withGentooPackages(t)
			asRoot(t, test.root)

			err := dependenciesInstalledOn(InstallCtx{Dynamic: test.dynamic}, binary.ProgramCe,
				test.family)

			var missing missingPackagesError

			require.ErrorAs(t, err, &missing)
			assert.Contains(t, err.Error(), test.want)
			assert.NotContains(t, err.Error(), "Unknown OS")
			assert.True(t, strings.HasSuffix(err.Error(),
				"\nUse --force if you already have those packages installed"), err.Error())
		})
	}
}

// TestDependenciesInstalled pins that a system with the programs and the
// packages of the build passes the check, and that the package managers
// are asked for the packages with no program: pacman -Q of Arch Linux,
// apk info -e of Alpine Linux, the package database of Gentoo.
func TestDependenciesInstalled(t *testing.T) {
	asRoot(t, false)

	t.Run("Arch Linux", func(t *testing.T) {
		commands := withPrograms(t, buildPrograms...)
		calls := fakePackageManager(t, commands, "pacman", "openssl", "readline", "ncurses", "zlib")

		// The static build needs no package with no program.
		require.NoError(t, dependenciesInstalledOn(InstallCtx{}, binary.ProgramCe, osFamilyArch))
		assert.NoFileExists(t, calls)

		err := dependenciesInstalledOn(InstallCtx{Dynamic: true}, binary.ProgramCe,
			osFamilyArch)
		require.EqualError(t, err, "Missing packages: icu\n"+
			"You can install them by running command:\n sudo pacman -S --needed icu\n"+
			"Use --force if you already have those packages installed")

		asked, err := os.ReadFile(calls)
		require.NoError(t, err)
		assert.Equal(t, "-Q openssl\n-Q readline\n-Q ncurses\n-Q icu\n-Q zlib\n", string(asked))
	})

	t.Run("Alpine Linux", func(t *testing.T) {
		commands := withPrograms(t, buildPrograms...)
		calls := fakePackageManager(t, commands, "apk")

		err := dependenciesInstalledOn(InstallCtx{}, binary.ProgramCe, osFamilyAlpine)
		require.ErrorContains(t, err, "Missing packages: linux-headers\n"+
			"You can install them by running command:\n sudo apk add linux-headers\n")

		fakePackageManager(t, commands, "apk", "linux-headers")
		require.NoError(t, dependenciesInstalledOn(InstallCtx{}, binary.ProgramCe,
			osFamilyAlpine))

		asked, err := os.ReadFile(calls)
		require.NoError(t, err)
		assert.Equal(t, "info -e linux-headers\n", string(asked))
	})

	t.Run("Alpine Linux without g++", func(t *testing.T) {
		commands := withPrograms(t, slices.DeleteFunc(slices.Clone(buildPrograms),
			func(name string) bool { return name == "g++" })...)
		fakePackageManager(t, commands, "apk", "linux-headers")

		err := dependenciesInstalledOn(InstallCtx{}, binary.ProgramCe, osFamilyAlpine)
		require.ErrorContains(t, err, "Missing packages: g++\n")
	})

	t.Run("Gentoo", func(t *testing.T) {
		withPrograms(t, buildPrograms...)
		withGentooPackages(t, "dev-libs/openssl-3.5.9", "sys-libs/readline-8.3_p6",
			"sys-libs/ncurses-6.5_p20251220", "sys-libs/zlib-1.3.2-r1",
			"dev-libs/icu-layoutex-76.1")

		require.NoError(t, dependenciesInstalledOn(InstallCtx{}, binary.ProgramCe,
			osFamilyGentoo))

		// icu-layoutex is another package than icu.
		err := dependenciesInstalledOn(InstallCtx{Dynamic: true}, binary.ProgramCe,
			osFamilyGentoo)
		require.ErrorContains(t, err, "Missing packages: dev-libs/icu\n"+
			"You can install them by running command:\n"+
			" sudo emerge --ask --noreplace dev-libs/icu\n")
	})

	t.Run("Debian, one package for two programs", func(t *testing.T) {
		withPrograms(t, slices.DeleteFunc(slices.Clone(buildPrograms),
			func(name string) bool { return name == "gcc" || name == "g++" })...)

		err := dependenciesInstalledOn(InstallCtx{}, binary.ProgramCe, osFamilyDebian)
		require.ErrorContains(t, err, "Missing packages: build-essential coreutils sed perl\n")
	})
}

// withOSRelease makes release the os-release of the host, for the test.
func withOSRelease(t *testing.T, release string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "os-release")
	require.NoError(t, os.WriteFile(path, []byte(release), 0o644))

	previous := osReleasePaths

	t.Cleanup(func() { osReleasePaths = previous })

	osReleasePaths = []string{path}
}

// answering returns a Confirm that answers confirmed and err, and the
// questions it is asked.
func answering(confirmed bool, err error) (func(string) (bool, error), *[]string) {
	questions := []string{}

	return func(question string) (bool, error) {
		questions = append(questions, question)

		return confirmed, err
	}, &questions
}

// unknownOSQuestion is the question of a build of Tarantool on an OS tt
// does not know the packages of.
const unknownOSQuestion = "Unknown OS, can't check if dependencies are installed.\n" +
	"Proceed without checking?"

// TestUnknownOSConfirmation pins the question of a build of Tarantool on an
// OS tt does not know the packages of: yes builds without the check, no
// aborts, and a confirmation that cannot be asked fails pointing at --yes
// and --force. A build of tt asks nothing: it needs the same everywhere.
func TestUnknownOSConfirmation(t *testing.T) {
	// What the Confirm of tt binary install fails with off a terminal.
	notTerminal := errors.New("no answer to the confirmation: the standard input is " +
		"not a terminal; pass --yes to confirm without a prompt")

	t.Run("yes", func(t *testing.T) {
		confirm, questions := answering(true, nil)

		require.NoError(t, dependenciesInstalledOn(InstallCtx{Confirm: confirm},
			binary.ProgramCe, osFamilyUnknown))
		assert.Equal(t, []string{unknownOSQuestion}, *questions)
	})

	t.Run("no", func(t *testing.T) {
		confirm, _ := answering(false, nil)

		err := dependenciesInstalledOn(InstallCtx{Confirm: confirm}, binary.ProgramCe,
			osFamilyUnknown)
		require.ErrorIs(t, err, binary.ErrCmdAbort)
	})

	t.Run("not asked", func(t *testing.T) {
		confirm, _ := answering(false, notTerminal)

		err := dependenciesInstalledOn(InstallCtx{Confirm: confirm}, binary.ProgramCe,
			osFamilyUnknown)
		require.ErrorIs(t, err, errUnknownOS)
		require.ErrorIs(t, err, notTerminal)
		require.EqualError(t, err, "cannot check the build dependencies on an unknown OS: "+
			"no answer to the confirmation: the standard input is not a terminal; "+
			"pass --yes to confirm without a prompt, or --force to skip the check")
	})

	t.Run("no Confirm", func(t *testing.T) {
		err := dependenciesInstalledOn(InstallCtx{}, binary.ProgramCe, osFamilyUnknown)
		require.ErrorIs(t, err, errNoConfirmation)
		require.ErrorContains(t, err, "--force")
	})

	t.Run("tt", func(t *testing.T) {
		withPrograms(t, mageExec, gitExec)

		confirm, questions := answering(false, notTerminal)

		require.NoError(t, dependenciesInstalledOn(InstallCtx{Confirm: confirm},
			binary.ProgramTt, osFamilyUnknown))
		assert.Empty(t, *questions)
	})
}

// TestForceSkipsTheDependencyCheck pins that --force skips the dependency
// check of a build of Tarantool from source and, on an OS tt does not know
// the packages of, its question; without --force a question not answered
// fails the build before it begins.
func TestForceSkipsTheDependencyCheck(t *testing.T) {
	if runtime.GOOS != osLinux {
		t.Skip("os-release tells the OS on Linux only")
	}

	withOSRelease(t, voidRelease)
	withPrograms(t)

	for _, force := range []bool{false, true} {
		t.Run("--force "+strconv.FormatBool(force), func(t *testing.T) {
			confirm, questions := answering(false, errors.New("not a terminal"))

			// A build that goes on looks for a tarantool/ the local
			// repository has not.
			installCtx := InstallCtx{
				Program: binary.ProgramCe, Force: force, Confirm: confirm, LocalRepo: t.TempDir(),
			}

			_, err := prepareTarantoolFromSource(installCtx, t.TempDir(),
				tagInstallVersion("3.9.0"))
			if force {
				require.ErrorIs(t, err, errDistfilesDirectoryNotFound)
				assert.Empty(t, *questions)

				return
			}

			require.ErrorIs(t, err, errUnknownOS)
			assert.Equal(t, []string{unknownOSQuestion}, *questions)
		})
	}
}

// TestMasterUpdateConfirmation pins the question of an installation of
// master over an installed one: a no keeps the installed master, and so does
// a confirmation that cannot be asked, under --no-prompt or off a terminal,
// with no failure. Nothing is asked about another version or a file that
// is no executable.
func TestMasterUpdateConfirmation(t *testing.T) {
	notAsked := errors.New("--no-prompt asks no questions")

	for name, test := range map[string]struct {
		kind       versionKind
		executable bool
		err        error
		asked      bool
	}{
		"not asked":      {versionKindMaster, true, notAsked, true},
		"no":             {versionKindMaster, true, nil, true},
		"a release":      {versionKindTag, true, nil, false},
		"not executable": {versionKindMaster, false, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			confirm, questions := answering(false, test.err)

			update, err := isUpdatePossible(InstallCtx{Program: binary.ProgramCe, Confirm: confirm},
				filepath.Join(t.TempDir(), "tarantool"), test.kind, test.executable)
			require.NoError(t, err)
			assert.False(t, update)

			if !test.asked {
				assert.Empty(t, *questions)

				return
			}

			require.Len(t, *questions, 1)
			assert.Contains(t, (*questions)[0], "The 'master' version of the tarantool")
		})
	}

	update, err := isUpdatePossible(InstallCtx{Program: binary.ProgramTt},
		filepath.Join(t.TempDir(), "tt"), versionKindMaster, true)
	require.NoError(t, err)
	assert.False(t, update, "with no Confirm")
}

func TestIsGentooPackageInstalled(t *testing.T) {
	withGentooPackages(t, "dev-build/automake-wrapper-20260920", "dev-build/cmake-4.1.2",
		"sys-devel/gcc-16.2.1_p20260926", "dev-vcs/-MERGING-git-2.51.0")
	require.NoError(t, os.WriteFile(filepath.Join(gentooPackageDB, "dev-build", "make-4.4.1"),
		nil, 0o644))

	for atom, want := range map[string]bool{
		"dev-build/cmake":    true,
		"sys-devel/gcc":      true,
		"dev-build/automake": false, // automake-wrapper is another package.
		"dev-vcs/git":        false, // A merge that has not ended.
		"dev-build/make":     false, // A file, not the directory of a package.
		"dev-util/cmake":     false, // Another category.
		"dev-lang/perl":      false,
		"cmake":              false, // An atom has a category.
	} {
		assert.Equal(t, want, isGentooPackageInstalled(atom), atom)
	}
}
