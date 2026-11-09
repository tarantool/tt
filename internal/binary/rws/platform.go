package rws

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	muslLibraryMarker        = "musl"
	gcompatLibraryMarker     = "gcompat"
	executablePermissionMask = 0o111
)

// errUnsupportedPlatform reports a host the static RPMs do not run on.
var errUnsupportedPlatform = errors.New("unsupported platform")

// packagePlatforms are the hosts the static RPMs run on, by GOARCH: the
// glibc loader and the libraries the executable needs.
var packagePlatforms = map[string]struct {
	loaderPath string   // Relative to the system root.
	libraries  []string // Relative to the resolved loader's directory.
}{
	goarchAMD64: {
		loaderPath: "lib64/ld-linux-x86-64.so.2",
		libraries:  []string{"libc.so.6", "libgcc_s.so.1"},
	},
	goarchARM64: {
		loaderPath: "lib/ld-linux-aarch64.so.1",
		libraries:  []string{"libc.so.6", "libgcc_s.so.1"},
	},
}

// checkPlatform checks for the loader, libc and libgcc used by the prebuilt RPMs.
// It only inspects file metadata and symlinks. systemRoot is "/" on the host.
func checkPlatform(goarch, systemRoot string) error {
	platform, supported := packagePlatforms[goarch]
	if !supported {
		return fmt.Errorf("%w: unsupported architecture linux/%s",
			errUnsupportedPlatform, goarch)
	}

	unavailable := fmt.Errorf("%w: prebuilt Tarantool requires the glibc loader /%s "+
		"and libraries %s", errUnsupportedPlatform, platform.loaderPath,
		strings.Join(platform.libraries, ", "))

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
			return fmt.Errorf("%w: /%s uses %s or %s", errUnsupportedPlatform,
				platform.loaderPath, muslLibraryMarker, gcompatLibraryMarker)
		}
	}

	return nil
}
