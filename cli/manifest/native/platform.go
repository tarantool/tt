// Package native tells native files - ELF and Mach-O binaries - by their
// content, and reports what they need to run: the platforms they run on and,
// for an ELF program, the loader and the shared libraries it asks for.
//
// tt package pack labels an archive with the platform its native files pin it
// to, and tt package build checks that what a component built into
// .rocks/bin/ is a program of the platform it built for, static when the
// manifest asks for that, and that a module asked to be static needs no
// library Tarantool may not have. They read a file the same way, so the
// detector lives here rather than in any of them.
package native

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
)

// Platform is an operating system and CPU architecture pair, spelled the way
// Go spells GOOS and GOARCH. For the Supported pairs its String form is the
// archive's platform token: linux-amd64, linux-arm64, darwin-amd64,
// darwin-arm64.
type Platform struct {
	OS   string
	Arch string
}

// String renders the pair as a token, "<os>-<arch>".
func (p Platform) String() string {
	return p.OS + "-" + p.Arch
}

// Supported lists the platforms tt builds and packs packages for: the
// tokens of [platform].platforms of a manifest, and the OSes a per-OS part
// of a build may be for (SupportedOS).
func Supported() []Platform {
	return []Platform{
		{OS: linuxOS, Arch: archAmd64},
		{OS: linuxOS, Arch: archArm64},
		{OS: darwinOS, Arch: archAmd64},
		{OS: darwinOS, Arch: archArm64},
	}
}

// SupportedOS lists the OSes of the Supported platforms, each once, in
// their order: linux, darwin.
func SupportedOS() []string {
	var oses []string

	for _, platform := range Supported() {
		if !slices.Contains(oses, platform.OS) {
			oses = append(oses, platform.OS)
		}
	}

	return oses
}

// Host returns the platform of the machine tt runs on, the one tt builds and
// packs for unless tt package build --target names another.
func Host() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// ErrUnknownPlatform reports a platform ParsePlatform does not take: a
// spelling of none, or one tt does not build for.
var ErrUnknownPlatform = errors.New("unknown platform")

// osAliases and archAliases are the other names ParsePlatform takes of an OS
// and an architecture: those of LuaRocks and of uname.
var (
	osAliases   = map[string]string{"macos": darwinOS, "macosx": darwinOS}
	archAliases = map[string]string{"x86_64": archAmd64, "aarch64": archArm64}
)

// ParsePlatform parses a platform as tt package build --target names one:
// <os>/<arch>, as Go spells GOOS/GOARCH, linux/arm64, or <os>-<arch>, as the
// token of an archive and [platform].platforms spell it, linux-arm64. The
// OS is linux or darwin, macos and macosx being darwin; the architecture
// amd64 or arm64, x86_64 and aarch64 being those; in any case. The platform
// is to be one tt builds for (Supported): another is ErrUnknownPlatform.
func ParsePlatform(text string) (Platform, error) {
	spelled := strings.ToLower(text)

	osName, arch, ok := strings.Cut(spelled, "/")
	if !ok {
		osName, arch, _ = strings.Cut(spelled, "-")
	}

	if alias, ok := osAliases[osName]; ok {
		osName = alias
	}

	if alias, ok := archAliases[arch]; ok {
		arch = alias
	}

	platform := Platform{OS: osName, Arch: arch}
	if slices.Contains(Supported(), platform) {
		return platform, nil
	}

	names := make([]string, 0, len(Supported()))
	for _, supported := range Supported() {
		names = append(names, supported.OS+"/"+supported.Arch)
	}

	last := len(names) - 1

	return Platform{OS: "", Arch: ""}, fmt.Errorf("%w %q: want %s or %s", ErrUnknownPlatform,
		text, strings.Join(names[:last], ", "), names[last])
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
