// Package native tells native files - ELF and Mach-O binaries - by their
// content, and reports what they need to run: the platforms they run on and,
// for an ELF program, the loader and the shared libraries it asks for.
//
// tt package pack labels an archive with the platform its native files pin it
// to, and tt package build checks that what a component built into
// .rocks/bin/ is a program of the platform it built for, static when the
// manifest asks for that. Both read a file the same way, so the detector
// lives here rather than in either of them.
package native

import (
	"runtime"
	"strings"
)

// Platform is an operating system and CPU architecture pair, spelled the way
// Go spells GOOS and GOARCH. For the four supported pairs its String form is
// the archive's platform token: linux-amd64, linux-arm64, darwin-amd64,
// darwin-arm64.
type Platform struct {
	OS   string
	Arch string
}

// String renders the pair as a token, "<os>-<arch>".
func (p Platform) String() string {
	return p.OS + "-" + p.Arch
}

// Host returns the platform of the machine tt runs on, the only one tt
// builds and packs for until it learns to build for another.
func Host() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// Describe renders the platforms of a native file for a message:
// "linux-arm64", or "darwin-amd64 and darwin-arm64" for a universal one.
func Describe(platforms []Platform) string {
	names := make([]string, 0, len(platforms))
	for _, p := range platforms {
		names = append(names, p.String())
	}

	last := len(names) - 1
	if last < 1 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:last], ", ") + " and " + names[last]
}
