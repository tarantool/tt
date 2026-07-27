package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
)

// Per-package metadata file names inside <tree>/manifests/<pkg>/.
const (
	MetaManifestFile = "manifest.toml"
	MetaLockFile     = "lock.toml"
	MetaVersionFile  = "VERSION"
	// PrimaryManifestFile / PrimaryLockFile are the primary package's files in
	// the install-root itself, not under manifests/. PrimaryVersionFile is the
	// optional SemVer pin the version derivation also honours.
	PrimaryManifestFile = "app.manifest.toml"
	PrimaryLockFile     = "app.manifest.lock"
	PrimaryVersionFile  = "VERSION"
)

const (
	// metaFilePerm is the mode metadata files are written with: world-readable,
	// matching the rocks tree they sit beside.
	metaFilePerm = 0o644
	// metaDirPerm is the mode metadata directories are created with.
	metaDirPerm = 0o755
)

// Package is one package present in a scope: the primary package the project
// develops (project scope only) or a guest installed from an archive. Its
// manifest and lock drive collision checks, the joint resolution of shared
// dependencies at install time, and the ownership ledger at uninstall time.
type Package struct {
	// Name is the package name.
	Name string
	// Manifest is its parsed manifest; nil if unreadable (a guest may predate a
	// manifest-writing tt, and the primary package need not exist).
	Manifest *manifest.Manifest
	// Lock is its parsed lock; nil if unreadable.
	Lock *manifest.Lock
	// Version is the recorded version string, for --upgrade comparisons and for
	// the list inventory. Empty when nothing recorded one.
	Version string
	// Primary marks the project's own package, which uninstall refuses to touch.
	Primary bool
}

// Packages enumerates every package present in the scope: the primary package
// (project scope, when <root>/app.manifest.toml exists) plus every guest under
// manifests/. A guest whose metadata is missing or unreadable is skipped rather
// than failing the whole call — a half-written earlier install must not wedge
// every later one, nor make the inventory unreadable.
//
// The primary package, when present, comes first; guests follow in directory
// order, which os.ReadDir sorts by name.
func Packages(lay Layout, scope Scope) ([]Package, error) {
	var packages []Package

	if scope.HasPrimary() {
		if primary, ok := ReadPrimary(lay.Root); ok {
			packages = append(packages, primary)
		}
	}

	entries, err := os.ReadDir(lay.Manifests)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return packages, nil
		}

		return nil, fmt.Errorf("reading install state %s: %w", lay.Manifests, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		guest, ok := ReadGuest(lay.Manifests, entry.Name())
		if ok {
			packages = append(packages, guest)
		}
	}

	return packages, nil
}

// Find returns the package with the given name, if any.
func Find(packages []Package, name string) (Package, bool) {
	for _, p := range packages {
		if p.Name == name {
			return p, true
		}
	}

	var zero Package

	return zero, false
}

// FindPrimary returns the project's primary package among the set, requiring a
// readable manifest — a primary with no manifest constrains nothing.
func FindPrimary(packages []Package) (Package, bool) {
	for _, pkg := range packages {
		if pkg.Primary && pkg.Manifest != nil {
			return pkg, true
		}
	}

	var zero Package

	return zero, false
}

// ReadPrimary reads the project's own package from the install-root. The
// manifest is optional (a bare deploy directory has none); when present its lock
// and VERSION file beside it are read too.
func ReadPrimary(root string) (Package, bool) {
	var zero Package

	//nolint:gosec // Reads tt's own install-state files.
	manBytes, err := os.ReadFile(filepath.Join(root, PrimaryManifestFile))
	if err != nil {
		return zero, false
	}

	man, _, err := manifest.ParseManifest(manBytes)
	if err != nil {
		return zero, false
	}

	pkg := Package{
		Name:     man.Package.Name,
		Manifest: man,
		Lock:     ReadLockFile(filepath.Join(root, PrimaryLockFile)),
		// The primary package's version is not recorded by an install — it is the
		// project's own. A VERSION pin next to the manifest is the one source that
		// can be read without shelling out to git, which an inventory listing has
		// no business doing.
		Version: readVersionFile(filepath.Join(root, PrimaryVersionFile)),
		Primary: true,
	}

	return pkg, true
}

// ReadGuest reads one installed guest's metadata from manifests/<name>/.
func ReadGuest(manifestsDir, name string) (Package, bool) {
	var zero Package

	dir := filepath.Join(manifestsDir, name)

	//nolint:gosec // Reads tt's own install-state files.
	manBytes, err := os.ReadFile(filepath.Join(dir, MetaManifestFile))
	if err != nil {
		return zero, false
	}

	man, _, err := manifest.ParseManifest(manBytes)
	if err != nil {
		return zero, false
	}

	pkg := Package{
		Name:     man.Package.Name,
		Manifest: man,
		Lock:     ReadLockFile(filepath.Join(dir, MetaLockFile)),
		Version:  readVersionFile(filepath.Join(dir, MetaVersionFile)),
		Primary:  false,
	}

	return pkg, true
}

// ReadLockFile parses a lock file, returning nil when it is absent or unreadable
// — a guest may predate a lock-writing tt, and the primary package need not have
// a lock at all.
func ReadLockFile(path string) *manifest.Lock {
	data, err := os.ReadFile(path) //nolint:gosec // Reads tt's own install-state files.
	if err != nil {
		return nil
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil
	}

	return lock
}

// readVersionFile reads a VERSION file, returning "" when it is absent.
func readVersionFile(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // Reads tt's own install-state files.
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

// LockedVersion returns the version a lock pins for a dependency, across all its
// products. A lock normally carries one product's closure, but nothing forbids
// several, and a dependency pinned in any of them is still brought in.
func LockedVersion(lock *manifest.Lock, dep string) (string, bool) {
	if lock == nil {
		return "", false
	}

	for _, prod := range lock.Products {
		for _, d := range prod.Dependencies {
			if d.Name == dep {
				return d.Version, true
			}
		}
	}

	return "", false
}

// LockedDependencies lists every dependency a lock pins, deduplicated by name
// across products and in no particular order. It is the set of rocks the
// package brought into the tree, which is what ownership is counted over.
func LockedDependencies(lock *manifest.Lock) map[string]string {
	pinned := map[string]string{}

	if lock == nil {
		return pinned
	}

	for _, prod := range lock.Products {
		for _, dep := range prod.Dependencies {
			if _, seen := pinned[dep.Name]; !seen {
				pinned[dep.Name] = dep.Version
			}
		}
	}

	return pinned
}

// WriteMetadata records a package's metadata under manifests/<name>/: the
// manifest, the lock and the version. This is the inventory list reads and the
// ledger uninstall's refcounting stands on — a dependency is owned by every
// package whose lock lists it.
func WriteMetadata(lay Layout, name string, manifestBytes, lockBytes []byte, version string) error {
	dir := filepath.Join(lay.Manifests, name)

	err := os.MkdirAll(dir, metaDirPerm)
	if err != nil {
		return fmt.Errorf("creating metadata directory: %w", err)
	}

	files := []struct {
		name string
		data []byte
	}{
		{MetaManifestFile, manifestBytes},
		{MetaLockFile, lockBytes},
		{MetaVersionFile, []byte(version + "\n")},
	}

	for _, file := range files {
		// Install metadata is world-readable by design, matching the rocks tree.
		path := filepath.Join(dir, file.name)

		err := os.WriteFile(path, file.data, metaFilePerm)
		if err != nil {
			return fmt.Errorf("writing %s metadata: %w", file.name, err)
		}
	}

	return nil
}

// RemoveMetadata deletes a package's manifests/<name>/ directory. Because
// ownership is derived from the locks left under manifests/, this single
// removal is what drops the package from every dependency's owner set.
func RemoveMetadata(lay Layout, name string) error {
	err := os.RemoveAll(filepath.Join(lay.Manifests, name))
	if err != nil {
		return fmt.Errorf("removing %s metadata: %w", name, err)
	}

	return nil
}
