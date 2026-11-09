package install

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errUnknownOS = errors.New("unknown OS")
)

const (
	// osDarwin is the name of macOS as runtime.GOOS reports it.
	osDarwin = "darwin"
	// osLinux is the name of Linux as runtime.GOOS reports it.
	osLinux = "linux"

	archAMD64 = "amd64"
	archARM64 = "arm64"
)

// checkTtPackagePlatform recognizes the platforms published in GitHub Releases.
// tt 2 publishes macOS archives for amd64, tt 3 for arm64.
func checkTtPackagePlatform(version, goos, goarch string) error {
	darwinArch := archARM64

	majorVersion, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")

	major, err := strconv.Atoi(majorVersion)
	if err == nil && major < 3 {
		darwinArch = archAMD64
	}

	supported := goos == osLinux && (goarch == archAMD64 || goarch == archARM64) ||
		goos == osDarwin && goarch == darwinArch
	if !supported {
		return fmt.Errorf("%w: no prebuilt tt %s for %s/%s; "+
			"use --source-build to build from source",
			errPrebuiltPackageUnavailable, version, goos, goarch)
	}

	return nil
}

// DistroInfo is a struct containing info about linux distro.
type DistroInfo struct {
	Name         string
	Vendor       string
	Version      string
	Architecture string
}

var (
	// PrettyNameRe is a regexp for PrettyName in os-release file.
	PrettyNameRe = regexp.MustCompile(`^PRETTY_NAME=(.*)$`)
	// IDRe is a regexp for ID in os-release file.
	IDRe = regexp.MustCompile(`^ID=(.*)$`)
	// VersionIDRe is a regexp for VersionID in os-release file.
	VersionIDRe = regexp.MustCompile(`^VERSION_ID=(.*)$`)
)

// getDistroInfo collects info about linux distro.
func getDistroInfo() (DistroInfo, error) {
	var (
		distroInfo DistroInfo
		err        error
	)

	// Get architecture.
	distroInfo.Architecture, err = binary.GetArch()
	if err != nil {
		return distroInfo, err
	}

	// Get distribution info.
	releaseFile, err := os.Open("/etc/os-release")
	if err != nil {
		return distroInfo, fmt.Errorf("failed to read OS release info: %w", err)
	}

	defer func() {
		_ = releaseFile.Close()
	}()

	scanner := bufio.NewScanner(releaseFile)
	for scanner.Scan() {
		if m := PrettyNameRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Name = strings.Trim(m[1], `"`)
		} else if m := IDRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Vendor = strings.Trim(m[1], `"`)
		} else if m := VersionIDRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Version = strings.Trim(m[1], `"`)
		}
	}

	return distroInfo, nil
}

// detectOsName returns name of the OS.
func detectOsName() (string, error) {
	if runtime.GOOS == osDarwin {
		return osDarwin, nil
	}

	if runtime.GOOS == "windows" {
		return "windows", nil
	}

	if runtime.GOOS == osLinux {
		distroInfo, err := getDistroInfo()
		return distroInfo.Name, err
	}

	return "", errUnknownOS
}
