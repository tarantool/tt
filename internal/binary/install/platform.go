package install

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
)

const (
	// osDarwin is the name of macOS as runtime.GOOS reports it.
	osDarwin = "darwin"
	// osLinux is the name of Linux as runtime.GOOS reports it.
	osLinux = "linux"

	archAMD64 = "amd64"
	archARM64 = "arm64"
)

// osFamily is a kind of system tt knows the packages of the dependencies of
// a build from source of: what checks they are installed and what installs
// them.
type osFamily int

const (
	// osFamilyUnknown is a system tt does not know the packages of.
	osFamilyUnknown osFamily = iota
	// osFamilyDarwin is macOS, with Homebrew.
	osFamilyDarwin
	// osFamilyDebian is Debian, Ubuntu and their derivatives: dpkg and apt.
	osFamilyDebian
	// osFamilyRedHat is RHEL, CentOS, Rocky Linux, AlmaLinux, Fedora and
	// their derivatives: rpm, and dnf or yum.
	osFamilyRedHat
	// osFamilyGentoo is Gentoo and its derivatives: Portage.
	osFamilyGentoo
	// osFamilyArch is Arch Linux and its derivatives, Manjaro and
	// EndeavourOS among them: pacman.
	osFamilyArch
	// osFamilyAlpine is Alpine Linux and its derivatives, such as
	// postmarketOS: apk. Its libc is musl, which the prebuilt Tarantool
	// does not run on, so Tarantool is built from source there.
	osFamilyAlpine
)

var (
	// osReleasePaths are where os-release(5) may be, in the order it is
	// looked for.
	osReleasePaths = []string{"/etc/os-release", "/usr/lib/os-release"}

	// familyIDs are the os-release IDs of the distributions of a family. A
	// derivative names the distribution it derives from in ID_LIKE: Manjaro
	// has ID_LIKE=arch. No ID is in two families.
	familyIDs = map[osFamily][]string{
		osFamilyDebian: {"debian", "ubuntu"},
		osFamilyRedHat: {"rhel", "centos", "fedora", "rocky", "almalinux", "ol", "amzn"},
		osFamilyGentoo: {"gentoo"},
		osFamilyArch:   {"arch"},
		osFamilyAlpine: {"alpine"},
	}
)

// osRelease is what os-release(5) tells of a distribution that tt reads:
// the ID of the distribution and those of the ones it derives from,
// ID_LIKE, all lowercase.
type osRelease struct {
	id   string
	like []string
}

// readOSRelease reads the os-release file at path.
func readOSRelease(path string) (osRelease, error) {
	file, err := os.Open(path)
	if err != nil {
		return osRelease{}, fmt.Errorf("failed to read OS release info: %w", err)
	}

	defer func() { _ = file.Close() }()

	var release osRelease

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !found {
			continue
		}

		value = strings.ToLower(strings.Trim(strings.TrimSpace(value), `"'`))

		switch key {
		case "ID":
			release.id = value
		case "ID_LIKE":
			release.like = strings.Fields(value)
		}
	}

	err = scanner.Err()
	if err != nil {
		return osRelease{}, fmt.Errorf("failed to read OS release info %s: %w", path, err)
	}

	return release, nil
}

// family returns the family of the distribution: that of its ID, else
// that of the first of the distributions it derives from with one.
func (release osRelease) family() osFamily {
	for _, id := range append([]string{release.id}, release.like...) {
		for family, ids := range familyIDs {
			if slices.Contains(ids, id) {
				return family
			}
		}
	}

	return osFamilyUnknown
}

// detectOsFamily returns the family of the host: macOS, or, on Linux, that
// of the distribution os-release describes.
func detectOsFamily() osFamily {
	switch runtime.GOOS {
	case osDarwin:
		return osFamilyDarwin
	case osLinux:
		for _, path := range osReleasePaths {
			release, err := readOSRelease(path)
			if err == nil {
				return release.family()
			}
		}
	}

	return osFamilyUnknown
}
