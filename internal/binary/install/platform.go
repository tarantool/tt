package install

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

	rpmArchAMD64 = "x86_64"
	rpmArchARM64 = "aarch64"

	muslLibraryMarker        = "musl"
	gcompatLibraryMarker     = "gcompat"
	executablePermissionMask = 0o111
)

var tarantoolPackagePlatforms = map[string]struct {
	loaderPath string   // Relative to the system root.
	libraries  []string // Relative to the resolved loader's directory.
}{
	archAMD64: {
		loaderPath: "lib64/ld-linux-x86-64.so.2",
		libraries:  []string{"libc.so.6", "libgcc_s.so.1"},
	},
	archARM64: {
		loaderPath: "lib/ld-linux-aarch64.so.1",
		libraries:  []string{"libc.so.6", "libgcc_s.so.1"},
	},
}

// checkTtPackagePlatform recognizes the platforms published in GitHub Releases.
func checkTtPackagePlatform(goos, goarch string) error {
	supported := goos == osLinux && (goarch == archAMD64 || goarch == archARM64) ||
		goos == osDarwin && goarch == archAMD64
	if !supported {
		return fmt.Errorf("%w: unsupported platform %s/%s; "+
			"use --source-build to build from source", errPrebuiltPackageUnavailable, goos, goarch)
	}

	return nil
}

// checkTarantoolPackagePlatform checks for the loader, libc and libgcc used by the prebuilt RPMs.
// It only inspects file metadata and symlinks. systemRoot is "/" on the host.
func checkTarantoolPackagePlatform(goarch, systemRoot string) error {
	platform, supported := tarantoolPackagePlatforms[goarch]
	if !supported {
		return fmt.Errorf("%w: unsupported architecture linux/%s; "+
			"use --source-build to build from source", errPrebuiltPackageUnavailable, goarch)
	}

	unavailable := fmt.Errorf("%w: prebuilt Tarantool requires the glibc loader /%s "+
		"and libraries %s; use --source-build to build from source",
		errPrebuiltPackageUnavailable, platform.loaderPath, strings.Join(platform.libraries, ", "))

	loaderFile, err := filepath.EvalSymlinks(filepath.Join(systemRoot, platform.loaderPath))
	if err != nil {
		return unavailable
	}

	files := []string{loaderFile}
	for _, library := range platform.libraries {
		files = append(files, filepath.Join(filepath.Dir(loaderFile), library))
	}

	for _, filename := range files {
		resolved, err := filepath.EvalSymlinks(filename)
		if err != nil {
			return unavailable
		}

		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() ||
			filename == loaderFile && info.Mode().Perm()&executablePermissionMask == 0 {
			return unavailable
		}

		name := filepath.Base(resolved)
		if strings.Contains(name, muslLibraryMarker) ||
			strings.Contains(name, gcompatLibraryMarker) {
			return fmt.Errorf("%w: /%s uses %s or %s; "+
				"use --source-build to build from source",
				errPrebuiltPackageUnavailable, platform.loaderPath,
				muslLibraryMarker, gcompatLibraryMarker)
		}
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
