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
	libraries  []string // In the resolved loader's directory or one of ld.so.conf.
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
	directories := append([]string{filepath.Dir(loaderFile)}, libraryDirs(systemRoot)...)

	for _, library := range platform.libraries {
		filename := findLibrary(directories, library)
		if filename == "" {
			return unavailable
		}

		files = append(files, filename)
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

// findLibrary returns the first of the directories that has the library, as
// the loader looks for it, or "" if none of them has it.
func findLibrary(directories []string, library string) string {
	for _, directory := range directories {
		filename := filepath.Join(directory, library)

		_, err := os.Lstat(filename)
		if err == nil {
			return filename
		}
	}

	return ""
}

// maxLdSoConfIncludes bounds the files ld.so.conf includes, in case one of
// them includes itself.
const maxLdSoConfIncludes = 64

// libraryDirs returns the directories /etc/ld.so.conf of the system root,
// and the files it includes, give the loader to look for libraries in, in
// their order. Most distributions keep libgcc_s next to libc, in the
// directory of the loader; Gentoo keeps it in the directory of GCC, which
// only ld.so.conf names.
func libraryDirs(systemRoot string) []string {
	var (
		directories []string
		read        int
		parse       func(filename string)
	)

	parse = func(filename string) {
		if read >= maxLdSoConfIncludes {
			return
		}

		read++

		data, err := os.ReadFile(filepath.Join(systemRoot, filename))
		if err != nil {
			return
		}

		for line := range strings.Lines(string(data)) {
			line, _, _ = strings.Cut(line, "#")

			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}

			if fields[0] != "include" {
				if filepath.IsAbs(fields[0]) {
					directories = append(directories, filepath.Join(systemRoot, fields[0]))
				}

				continue
			}

			// include takes glob patterns, relative to the directory of the
			// file that includes them.
			for _, pattern := range fields[1:] {
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(filepath.Dir(filename), pattern)
				}

				matches, _ := filepath.Glob(filepath.Join(systemRoot, pattern))
				for _, match := range matches {
					relative, err := filepath.Rel(systemRoot, match)
					if err == nil {
						parse(string(filepath.Separator) + relative)
					}
				}
			}
		}
	}

	parse("/etc/ld.so.conf")

	return directories
}
