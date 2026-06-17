package manifest

import (
	"bytes"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// LockVersion is the lock format version this build of tt writes.
const LockVersion = "0.1"

// Lock is the parsed form of app.manifest.lock. It records the exact versions
// and hashes tt resolved, one snapshot per product. The dependency fields are
// filled by the resolver; this layer only owns the shape and serialization.
type Lock struct {
	LockVersion      string `toml:"lock_version"`
	ManifestVersion  string `toml:"manifest_version"`
	GeneratedBy      string `toml:"generated_by"`  // Like "tt 3.x.y".
	ManifestHash     string `toml:"manifest_hash"` // Like "sha256:...".
	BundledTarantool string `toml:"bundled_tarantool_version,omitempty"`
	BundledTt        string `toml:"bundled_tt_version,omitempty"`
	BundledTcm       string `toml:"bundled_tcm_version,omitempty"`

	// Products maps product name to its resolution snapshot. On disk this is
	// the [lock.products.<name>] table tree.
	Products map[string]LockProduct `toml:"-"`
}

// LockProduct is one product's resolution snapshot - the full transitive
// closure resolved for that product.
type LockProduct struct {
	Dependencies []LockDependency `toml:"dependencies"`
}

// LockDependency is one resolved dependency inside a product snapshot
// ([[lock.products.<name>.dependencies]]).
type LockDependency struct {
	Name        string `toml:"name"`
	Version     string `toml:"version"`                // Exact version.
	Source      string `toml:"source"`                 // Registry or path.
	Checksum    string `toml:"checksum,omitempty"`     // Registry: "md5:..." or "sha256:...".
	Path        string `toml:"path,omitempty"`         // Path source.
	ContentHash string `toml:"content_hash,omitempty"` // Path: sha256 of contents.
}

// lockWire is the on-disk shape of the lock. It nests Products under a [lock]
// table so the file reads [lock.products.<name>]; go-toml does not split dotted
// struct tags, so the nesting is expressed with a real struct.
type lockWire struct {
	LockVersion      string `toml:"lock_version"`
	ManifestVersion  string `toml:"manifest_version"`
	GeneratedBy      string `toml:"generated_by"`
	ManifestHash     string `toml:"manifest_hash"`
	BundledTarantool string `toml:"bundled_tarantool_version,omitempty"`
	BundledTt        string `toml:"bundled_tt_version,omitempty"`
	BundledTcm       string `toml:"bundled_tcm_version,omitempty"`
	Lock             struct {
		Products map[string]LockProduct `toml:"products,omitempty"`
	} `toml:"lock"`
}

// ParseLock parses app.manifest.lock bytes into a Lock. A newer major
// lock_version is refused; unknown fields are tolerated (the lock is tt-owned
// state, not a hand-authored evolution surface).
func ParseLock(data []byte) (*Lock, error) {
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

	var wire lockWire

	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.EnableUnmarshalerInterface()

	err = dec.Decode(&wire)
	if err != nil {
		return nil, fmt.Errorf("parsing lock: %w", err)
	}

	lock := wire.toLock()

	return &lock, nil
}

// Marshal serializes the lock to TOML in canonical form.
func (l Lock) Marshal() ([]byte, error) {
	data, err := toml.Marshal(l.toWire())
	if err != nil {
		return nil, fmt.Errorf("marshaling lock: %w", err)
	}

	return data, nil
}

func (l Lock) toWire() lockWire {
	var wire lockWire

	wire.LockVersion = l.LockVersion
	wire.ManifestVersion = l.ManifestVersion
	wire.GeneratedBy = l.GeneratedBy
	wire.ManifestHash = l.ManifestHash
	wire.BundledTarantool = l.BundledTarantool
	wire.BundledTt = l.BundledTt
	wire.BundledTcm = l.BundledTcm
	wire.Lock.Products = l.Products

	return wire
}

func (w lockWire) toLock() Lock {
	return Lock{
		LockVersion:      w.LockVersion,
		ManifestVersion:  w.ManifestVersion,
		GeneratedBy:      w.GeneratedBy,
		ManifestHash:     w.ManifestHash,
		BundledTarantool: w.BundledTarantool,
		BundledTt:        w.BundledTt,
		BundledTcm:       w.BundledTcm,
		Products:         w.Lock.Products,
	}
}
