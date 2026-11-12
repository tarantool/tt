package manifest

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

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

	// DevDependencies is the closure of [dev_dependencies], as
	// [[lock.dev_dependencies]]. It is one list rather than one per product:
	// the manifest declares dev dependencies globally, so there is nothing to
	// key them by.
	//
	// It is pinned like any other closure because it can shape the artifact:
	// the dev tree is materialized into .rocks/ before the component build
	// backends run, so a generator installed as a dev dependency writes files
	// that are then laid out and packed. Recording the versions is what makes
	// such a build reproducible. Empty for a manifest that declares none, and then absent
	// from the file entirely - a lock written before dev dependencies existed
	// is therefore indistinguishable from one whose manifest declares none,
	// which is why IsStale checks the manifest side too.
	DevDependencies []LockDependency `toml:"-"`
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
	Path        string `toml:"path,omitempty"`         // Path source.
	ContentHash string `toml:"content_hash,omitempty"` // Path: sha256 of contents.

	// Artifacts pins every file of a registry rock version by its arch, as a
	// rock server that publishes a tt-rocks-index.json lists them: the rock
	// of each platform ("linux-x86_64", "macosx-aarch64", "all", "src") and
	// the bare rockspec under "rockspec", each "sha256:<hex>". The install
	// checks the file it picks for its host against it, so a lock resolved
	// on one platform installs verified on every other; a host whose file is
	// not pinned has the lock resolved again. Empty, and absent from the
	// file, for a rock from a server that publishes no digests, which
	// installs unpinned as before.
	//
	// On disk it is an inline table, one line per dependency:
	// artifacts = { linux-x86_64 = 'sha256:...', rockspec = 'sha256:...' }.
	Artifacts map[string]string `toml:"artifacts,inline,omitempty"`
}

// artifactDigestPrefix starts every digest of LockDependency.Artifacts: the
// algorithm, so another one can be recorded without a new lock format.
const artifactDigestPrefix = "sha256:"

// ArtifactDigest renders a sha256 in hex as a digest of
// LockDependency.Artifacts.
func ArtifactDigest(sha256 string) string {
	return artifactDigestPrefix + strings.ToLower(sha256)
}

// ArtifactPins returns the digests of Artifacts as the sha256 hex by arch
// the rock installer takes (client.InstallOpts.Artifacts), or nil when the
// dependency pins no file: nil installs without pins, while an empty map
// would pin nothing and refuse every file. A digest that is not
// "sha256:<hex>" is an error naming the dependency, since installing past
// it would install unverified what the lock means to verify.
func (d LockDependency) ArtifactPins() (map[string]string, error) {
	if len(d.Artifacts) == 0 {
		return nil, nil //nolint:nilnil // Nil pins are the installer's "no pins".
	}

	pins := make(map[string]string, len(d.Artifacts))

	for arch, digest := range d.Artifacts {
		sum, ok := strings.CutPrefix(digest, artifactDigestPrefix)
		if !ok || !isSHA256Hex(sum) {
			return nil, fmt.Errorf("lock entry %s %s: %w", d.Name, d.Version,
				invalid("artifacts."+arch, "%q is not sha256:<64 hex digits>", digest))
		}

		pins[arch] = strings.ToLower(sum)
	}

	return pins, nil
}

// sha256HexLen is the length of a sha256 in hex.
const sha256HexLen = 64

// isSHA256Hex reports whether s is a sha256 in hex.
func isSHA256Hex(s string) bool {
	if len(s) != sha256HexLen {
		return false
	}

	_, err := hex.DecodeString(s)

	return err == nil
}

// lockWire is the on-disk shape of the lock. It nests Products and the dev
// closure under a [lock] table so the file reads [lock.products.<name>] and
// [[lock.dev_dependencies]]; go-toml does not split dotted struct tags, so the
// nesting is expressed with a real struct.
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
		// omitempty keeps the key out of a lock with no dev closure, so every
		// lock written before dev dependencies were resolved still round-trips
		// byte-for-byte.
		DevDependencies []LockDependency `toml:"dev_dependencies,omitempty"`
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
	wire.Lock.DevDependencies = l.DevDependencies

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
		DevDependencies:  w.Lock.DevDependencies,
	}
}
