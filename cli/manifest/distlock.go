package manifest

import (
	"bytes"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// DistributionLock is the parsed form of tt.manifest.lock: what a
// distribution of tt was last built from. Its envelope is the one of the
// application lock (lock_version, manifest_version, generated_by,
// manifest_hash); its body pins the core, the modules of the core's
// repository and every tt module to exact versions, and records how the
// binary was compiled. The checksums of every Go module are in
// tt.manifest.sum next to it.
type DistributionLock struct {
	LockVersion     string                  `toml:"lock_version"`
	ManifestVersion string                  `toml:"manifest_version"`
	GeneratedBy     string                  `toml:"generated_by"`  // Like "tt 3.x.y".
	ManifestHash    string                  `toml:"manifest_hash"` // Like "sha256:...".
	Core            LockedCore              `toml:"core"`
	Build           LockedBuild             `toml:"build"`
	Modules         map[string]LockedModule `toml:"modules,omitempty"`
}

// LockedCore is the core a distribution was built on ([core]).
type LockedCore struct {
	// Module is the Go module of the core.
	Module string `toml:"module"`
	// Version is the exact version of the core; empty for a local core.
	Version string `toml:"version,omitempty"`
	// Revision is the commit of the core's repository Version is.
	Revision string `toml:"revision,omitempty"`
	// Path is the local core, as the manifest names it.
	Path string `toml:"path,omitempty"`
	// ContentHash is the hash of the files of a local core and of the
	// modules of its repository it is built with, "sha256:<hex>"; a build
	// from the lock fails when they changed. Empty for a versioned core.
	ContentHash string `toml:"content_hash,omitempty"`
	// Repository maps every other module of the core's repository to its
	// exact version at Revision; empty for a local core, whose modules are
	// taken from its directory.
	Repository map[string]string `toml:"repository,omitempty"`
}

// LockedBuild is how the binary was compiled ([build]). It records the
// last build and does not gate the next one: the same pinned modules are
// compiled with whatever TLS implementation is asked for.
type LockedBuild struct {
	// Toolchain is the Go toolchain, as go env GOVERSION reports it.
	Toolchain string `toml:"toolchain"`
	// SSL is the TLS implementation.
	SSL string `toml:"ssl"`
	// Tags are the build tags.
	Tags []string `toml:"tags,omitempty"`
}

// LockedModule is one tt module of a distribution ([modules.<name>]).
type LockedModule struct {
	// Module is the Go module path.
	Module string `toml:"module"`
	// Version is the exact version; empty for a module of the core's
	// repository, which follows the core, and for a local module.
	Version string `toml:"version,omitempty"`
	// Path is the local module, as the manifest names it.
	Path string `toml:"path,omitempty"`
	// ContentHash is the hash of the files of a local module,
	// "sha256:<hex>"; a build from the lock fails when they changed. Empty
	// for a module from a module proxy, which go.sum checks.
	ContentHash string `toml:"content_hash,omitempty"`
}

// ParseDistributionLock parses tt.manifest.lock bytes. A newer major
// lock_version is refused; unknown fields are tolerated, as in the
// application lock: the lock is state tt writes, not a document people edit.
func ParseDistributionLock(data []byte) (*DistributionLock, error) {
	var head struct {
		LockVersion string `toml:"lock_version"`
	}

	err := toml.Unmarshal(data, &head)
	if err != nil {
		return nil, fmt.Errorf("parsing lock: %w", err)
	}

	if head.LockVersion == "" {
		return nil, invalid("lock_version", "is required")
	}

	declared, err := parseFormatVersion(head.LockVersion)
	if err != nil {
		return nil, err
	}

	if declared.major > ourLockVersion.major {
		return nil, fmt.Errorf("lock version %q %w (supports %d.x)",
			declared, ErrUnsupportedVersion, ourLockVersion.major)
	}

	lock := new(DistributionLock)

	err = toml.NewDecoder(bytes.NewReader(data)).Decode(lock)
	if err != nil {
		return nil, fmt.Errorf("parsing lock: %w", err)
	}

	return lock, nil
}

// Marshal serializes the lock to TOML in canonical form: the same lock is
// always the same bytes.
func (l *DistributionLock) Marshal() ([]byte, error) {
	data, err := toml.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("marshaling lock: %w", err)
	}

	return data, nil
}
