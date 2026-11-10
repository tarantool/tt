// Package analyzer puts the EmmyLua analyzer
// (github.com/EmmyLuaLs/emmylua-analyzer-rust) into a project and runs it
// with what tt knows of the project: the Tarantool it runs on, whose
// EmmyLua stubs are the types of the Tarantool API, and its rocks.
//
// The programs of a build - emmylua_check, emmylua_doc_cli, emmylua_ls and
// luafmt - are packed as a binary rock, emmylua, which the project declares
// in [dev_dependencies] and tt package installs into .rocks/bin like any
// other rock.
//
// It is a stopgap: the rock and the table of its releases belong on a rock
// server. Until one serves them, the builds the module knows about, and the
// checksums it holds them to, are written down here.
package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
)

// RockName is the name of the rock the programs are packed as, and what the
// project declares.
const RockName = "emmylua"

// sourceRepo is the repository of EmmyLua.
const sourceRepo = "https://github.com/EmmyLuaLs/emmylua-analyzer-rust"

// Program names one program of an EmmyLua build.
type Program string

const (
	// Check is the checker: the diagnostics of the language server, in batch.
	Check Program = "emmylua_check"
	// Doc generates the documentation of the annotated code.
	Doc Program = "emmylua_doc_cli"
	// LanguageServer is the language server.
	LanguageServer Program = "emmylua_ls"
	// Format is the formatter.
	Format Program = "luafmt"
)

// Programs are the programs a build has, and the rock carries in bin/.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var Programs = []Program{Check, Doc, LanguageServer, Format}

// Platform is the build of a release for one platform.
type Platform struct {
	// GOOS and GOARCH name the hosts the build runs on.
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	// Suffix ends the names of the release assets of the platform:
	// <program>-<suffix>.tar.gz, each holding the program alone.
	Suffix string `json:"suffix"`
	// SHA256 are the checksums of the assets, by program, as GitHub
	// publishes them.
	SHA256 map[Program]string `json:"sha256"`
}

// RockArch is the LuaRocks arch of the rock of the platform, as go-luarocks
// names the host's: linux-x86_64, macosx-aarch64.
func (p Platform) RockArch() string {
	return RockArch(p.GOOS, p.GOARCH)
}

// Asset is the file name of the release asset of program.
func (p Platform) Asset(program Program) string {
	return string(program) + "-" + p.Suffix + ".tar.gz"
}

// The hosts the releases have builds for.
const (
	goosLinux   = "linux"
	goosDarwin  = "darwin"
	goarchAMD64 = "amd64"
	goarchARM64 = "arm64"
)

// RockArch is the LuaRocks arch of goos/goarch.
func RockArch(goos, goarch string) string {
	osName := goos
	if goos == goosDarwin {
		osName = "macosx"
	}

	cpu := goarch

	switch goarch {
	case goarchAMD64:
		cpu = "x86_64"
	case goarchARM64:
		cpu = "aarch64"
	}

	return osName + "-" + cpu
}

// sums are the checksums of the assets of a platform, in the order of
// Programs.
func sums(check, doc, languageServer, format string) map[Program]string {
	return map[Program]string{
		Check: check, Doc: doc, LanguageServer: languageServer, Format: format,
	}
}

// Release is an EmmyLua build tt can put into a project.
type Release struct {
	// Version is the version of the rock, without its revision.
	Version string `json:"version"`
	// RockRevision is the revision of the rockspec tt writes for it.
	RockRevision int `json:"rock_revision"`
	// Ref is the git ref of EmmyLua the build is of: a tag or a commit.
	Ref string `json:"ref"`
	// SourceMD5 is the md5 of the GitHub archive of Ref: the source the
	// rockspec points at. It is the same for every platform, so the lock
	// records the same checksum on every host.
	SourceMD5 string `json:"source_md5"`
	// Platforms are the builds tt downloads. A release without them is
	// packed from a local build (tt emmylua prepare --from) or declared from
	// a rock server (--rock-server).
	Platforms []Platform `json:"platforms,omitempty"`
	// BaseURL is where the assets are: <base>/<asset>. Empty is the GitHub
	// release of Ref.
	BaseURL string `json:"base_url,omitempty"`
}

// ReleasesEnv names a JSON file of releases tt uses instead of its own
// table, by version: a build of one's own, the assets of a mirror.
const ReleasesEnv = "TT_EMMYLUA_RELEASES"

// DefaultVersion is the release a project is prepared with when none is
// named.
const DefaultVersion = "0.30.0pre1"

// releases are the releases tt knows, by version.
//
//   - 0.30.0pre1 is EmmyLua at a commit of main after 0.25.1, whose programs
//     report 0.30.0: it is not released, so it has no assets, and is packed
//     from a local build. pre1 sorts it after 0.25.1 and before the 0.30.0
//     EmmyLua will release.
//   - 0.25.1 is the release Tarantool checks its own code with.
//
//nolint:gochecknoglobals // A table of constants: Go has no constant structs.
var releases = map[string]Release{
	"0.30.0pre1": {
		Version:      "0.30.0pre1",
		RockRevision: 1,
		Ref:          "599c33862491c5ca0f325e0ef549ad34c510ca97",
		SourceMD5:    "2e5867117dccf42f0b36e8a751b592a8",
		Platforms:    nil,
		BaseURL:      "",
	},
	tarantoolVersion: {
		Version:      tarantoolVersion,
		RockRevision: 1,
		Ref:          tarantoolVersion,
		SourceMD5:    "ca05597790110ec49ef46e66cc377d5e",
		BaseURL:      "",
		Platforms: []Platform{
			{
				GOOS: goosLinux, GOARCH: goarchAMD64, Suffix: "linux-x64-glibc.2.17",
				SHA256: sums(
					"e4a700af4921e2d908e2f7299eaa86e3e1b3b265e27225ab33c7eb03ebc2187d",
					"f8e279988471e805e79839f51bb305647598b3e333609dd5ced177df722ad861",
					"9731ec340b1b40916555dcb31c1c539beee9fa184997962c9af250c42b316950",
					"bce370647ff33d2a62a1b9b3802d87b3b177383b20785a7af2348118fb70c727",
				),
			},
			{
				GOOS: goosLinux, GOARCH: goarchARM64, Suffix: "linux-aarch64-glibc.2.17",
				SHA256: sums(
					"62a9a56d3ff5d0e108909332cb91582a0c1a4ebbfdce1a4422e7ae74d4a7f8b1",
					"fefecee80e31620ed77ed889d8450e0e993db49daedc345c06c79ec34297878e",
					"210535be13c31ceefcdd0f8451eb4c0d3d430887fc2c382eaf82484a9187220d",
					"9a9e6dfbaa94f2b1378732b34fd9d7d553291f230eb2230defae1cff1279204e",
				),
			},
			{
				GOOS: goosDarwin, GOARCH: goarchAMD64, Suffix: "darwin-x64",
				SHA256: sums(
					"e91fe82c361d8dece8e2ce9aeb2313b8809b4714f72bbbeab5a27e754d8de2c6",
					"b8c98fab4cf28204821a55f5434363c86d69dcc4d9d80f72ba499e18a26924da",
					"abc9cbe2e8d28624e0d441fa55055b1196f73251b034356d717b0e58b562f182",
					"033d524551a869d0bf6ebabe00e1f4ccb697af0680d4c1c00eb0cb6f777d944f",
				),
			},
			{
				GOOS: goosDarwin, GOARCH: goarchARM64, Suffix: "darwin-arm64",
				SHA256: sums(
					"eeaad59d173cb8d7cb0fe778936d7b584fe114a3eb46587ef3e4aec24b605af4",
					"d21a74bfff5ff53d44888ede13ad38152696da1b6c5b1021f3c0c4a54d25db5e",
					"335023043cbc9ad683a13b24eb79f709a370215daf53673276b9a9663df96504",
					"1637fec3d449c10d71e27a30e0cc84bd4ad46b124c5ff8de0767cab954b67a4f",
				),
			},
		},
	},
}

// tarantoolVersion is the release Tarantool checks its own code with
// (.github/actions/setup-emmylua-analyzer of tarantool/tarantool).
const tarantoolVersion = "0.25.1"

// knownReleases returns the releases tt knows: its own, or those of the file
// ReleasesEnv names.
func knownReleases() (map[string]Release, error) {
	path := os.Getenv(ReleasesEnv)
	if path == "" {
		return releases, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // The file the user named.
	if err != nil {
		return nil, fmt.Errorf("reading %s (%s): %w", path, ReleasesEnv, err)
	}

	var table map[string]Release

	err = json.Unmarshal(data, &table)
	if err != nil {
		return nil, fmt.Errorf("reading %s (%s): %w", path, ReleasesEnv, err)
	}

	return table, nil
}

// LookupRelease returns the release of version.
func LookupRelease(version string) (Release, error) {
	known, err := knownReleases()
	if err != nil {
		return Release{}, err
	}

	release, ok := known[version]
	if !ok {
		return Release{}, fmt.Errorf("%w: EmmyLua %s; known: %s",
			ErrUnknownRelease, version, strings.Join(Versions(), ", "))
	}

	return release, nil
}

// Versions returns the releases tt knows, oldest first.
func Versions() []string {
	known, err := knownReleases()
	if err != nil {
		known = releases
	}

	versions := make([]string, 0, len(known))
	for version := range known {
		versions = append(versions, version)
	}

	slices.Sort(versions)

	return versions
}

// RockVersion is the version of the rock: <version>-<revision>.
func (r Release) RockVersion() string {
	return fmt.Sprintf("%s-%d", r.Version, r.RockRevision)
}

// SourceURL is the GitHub archive of Ref, which the rockspec names as the
// source of the rock.
func (r Release) SourceURL() string {
	return sourceRepo + "/archive/" + r.Ref + ".tar.gz"
}

// AssetURL is the download URL of the release asset named asset.
func (r Release) AssetURL(asset string) string {
	base := r.BaseURL
	if base == "" {
		base = sourceRepo + "/releases/download/" + r.Ref
	}

	return strings.TrimSuffix(base, "/") + "/" + asset
}

// HostPlatform returns the build of the release for the host tt runs on.
func (r Release) HostPlatform() (Platform, error) {
	return r.platform(runtime.GOOS, runtime.GOARCH)
}

// platform returns the build of the release for goos/goarch.
func (r Release) platform(goos, goarch string) (Platform, error) {
	for _, platform := range r.Platforms {
		if platform.GOOS == goos && platform.GOARCH == goarch {
			return platform, nil
		}
	}

	if len(r.Platforms) == 0 {
		return Platform{}, fmt.Errorf("%w: EmmyLua %s has no builds to download; "+
			"pack it from a build of %s (--from) or declare it from a rock server "+
			"(--rock-server)", ErrUnsupportedPlatform, r.Version, r.Ref)
	}

	supported := make([]string, 0, len(r.Platforms))
	for _, platform := range r.Platforms {
		supported = append(supported, platform.GOOS+"/"+platform.GOARCH)
	}

	return Platform{}, fmt.Errorf("%w: EmmyLua %s has no build for %s/%s; it has %s",
		ErrUnsupportedPlatform, r.Version, goos, goarch, strings.Join(supported, ", "))
}
