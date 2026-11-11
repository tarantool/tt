// Package wasm prepares a project for tarantool-wasm
// (github.com/tarantool/tarantool-wasm-rs) until tt builds WebAssembly
// components itself: it declares the wasm dependency - the binary rock of the
// native module, from a rock server, or packed from a local build into a
// registry inside the project - and puts tarawasm, which builds the
// components for now, where the project's builds find it.
//
// It is a stopgap, not the shape of the feature: the components belong to
// build backends of tt. The rock of every platform is served by a rock
// server, DefaultRockServer unless the project is told another; the releases
// tt knows about, and the checksums it holds their assets to, are written
// down here for what packs the rock itself: prepare --from and tt wasm rock.
package wasm

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
)

// releaseBaseURL is where the assets of a tarantool-wasm-rs release are
// downloaded from: <base>/v<version>/<asset>.
const releaseBaseURL = "https://github.com/tarantool/tarantool-wasm-rs/releases/download"

// RockName is the name of the rock the native module is packed as; it is what
// require('wasm') loads, and what the project declares.
const RockName = "wasm"

// Platform is one native build of a release.
type Platform struct {
	// GOOS and GOARCH name the hosts the build runs on.
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	// Asset is the file name of the release asset.
	Asset string `json:"asset"`
	// SHA256 is the asset's checksum, as GitHub publishes it for the asset.
	SHA256 string `json:"sha256"`
	// RockArch is the LuaRocks arch of the rock: what go-luarocks accepts as
	// the host's (linux-x86_64, macosx-aarch64).
	RockArch string `json:"rock_arch"`
	// Module is the file the rock carries under lib/: wasm.so, or wasm.dylib
	// on macOS, where Tarantool looks for C modules as ?.dylib only.
	Module string `json:"module"`
}

// Release is a tarantool-wasm-rs release tt can prepare a project for.
type Release struct {
	// Version is the release version, without the v of its tag.
	Version string `json:"version"`
	// RockRevision is the revision of the rockspec tt writes for it.
	RockRevision int `json:"rock_revision"`
	// SDKAsset and SDKMD5 name the release's SDK archive: the source the
	// rockspec points at. It is the one asset all platforms share, so the
	// rockspec, and the checksum the lock records from it, is the same on
	// every host.
	SDKAsset string `json:"sdk_asset"`
	SDKMD5   string `json:"sdk_md5"`
	// Platforms are the native builds of the release.
	Platforms []Platform `json:"platforms"`
	// BaseURL is where the assets are: <base>/v<version>/<asset>. Empty is
	// the releases of tarantool-wasm-rs on GitHub.
	BaseURL string `json:"base_url,omitempty"`
}

// ReleasesEnv names a JSON file of releases tt uses instead of its own
// table, by version: the assets of a mirror, say.
const ReleasesEnv = "TT_WASM_RELEASES"

// releases are the releases tt knows, by version. Each is checked against the
// assets published for it; a new one is added here by hand, with the
// checksums of its assets.
//
//nolint:gochecknoglobals // A table of constants: Go has no constant structs.
var releases = map[string]Release{
	"0.1.0": {
		Version:      "0.1.0",
		RockRevision: 1,
		SDKAsset:     "tarantool-wasm-0.1.0-sdk.tar.gz",
		SDKMD5:       "f986e42dd8d430110c06e9fb2963209b",
		BaseURL:      "",
		Platforms: []Platform{
			{
				GOOS:     "linux",
				GOARCH:   "amd64",
				Asset:    "tarantool-wasm-0.1.0-linux-x86_64.so",
				SHA256:   "65866215ce6424389381644ef65adc3906f896d46a03843f213baf16df758585",
				RockArch: "linux-x86_64",
				Module:   "wasm.so",
			},
			{
				GOOS:     "darwin",
				GOARCH:   "arm64",
				Asset:    "tarantool-wasm-0.1.0-macos-arm64.dylib",
				SHA256:   "bf2960db7dfe3535f7b6facecd2b33c4dbd9479f90b691e1b964a2499ef5a38d",
				RockArch: "macosx-aarch64",
				Module:   "wasm.dylib",
			},
		},
	},
}

// DefaultVersion is the release a project is prepared for when none is named.
const DefaultVersion = "0.1.0"

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
		return Release{}, fmt.Errorf("%w: tarantool-wasm %s; known: %s",
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

// HostPlatform returns the build of the release for the host tt runs on.
func (r Release) HostPlatform() (Platform, error) {
	return r.platform(runtime.GOOS, runtime.GOARCH)
}

// RockVersion is the version of the rock: <version>-<revision>.
func (r Release) RockVersion() string {
	return fmt.Sprintf("%s-%d", r.Version, r.RockRevision)
}

// AssetURL is the download URL of the release asset named asset.
func (r Release) AssetURL(asset string) string {
	base := r.BaseURL
	if base == "" {
		base = releaseBaseURL
	}

	return strings.TrimSuffix(base, "/") + "/v" + r.Version + "/" + asset
}

// platform returns the build of the release for goos/goarch.
func (r Release) platform(goos, goarch string) (Platform, error) {
	for _, platform := range r.Platforms {
		if platform.GOOS == goos && platform.GOARCH == goarch {
			return platform, nil
		}
	}

	supported := make([]string, 0, len(r.Platforms))
	for _, platform := range r.Platforms {
		supported = append(supported, platform.GOOS+"/"+platform.GOARCH)
	}

	return Platform{}, fmt.Errorf("%w: tarantool-wasm %s has no build for %s/%s; it has %s",
		ErrUnsupportedPlatform, r.Version, goos, goarch, strings.Join(supported, ", "))
}
