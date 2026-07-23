package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
)

// Per-package metadata file names inside .rocks/manifests/<pkg>/.
const (
	metaManifestFile = "manifest.toml"
	metaLockFile     = "lock.toml"
	metaVersionFile  = "VERSION"
	// primaryManifestFile / primaryLockFile are the primary package's files in
	// the install-root itself, not under manifests/.
	primaryManifestFile = manifestFileName
	primaryLockFile     = lockFileName
	// metaFilePerm is the mode metadata files are written with: world-readable,
	// matching the rocks tree they sit beside.
	metaFilePerm = 0o644
)

// installedPackage is one package already present in a scope: the primary
// package the project develops (project scope only) or a guest installed
// earlier. Its manifest and lock drive collision checks and the joint
// resolution of shared dependencies.
type installedPackage struct {
	// name is the package name.
	name string
	// manifest is its parsed manifest; nil if unreadable (a guest may predate a
	// manifest-writing tt, and the primary package need not exist).
	manifest *manifest.Manifest
	// lock is its parsed lock; nil if unreadable.
	lock *manifest.Lock
	// version is the recorded version string, for --upgrade comparisons.
	version string
	// primary marks the project's own package, which uninstall refuses to touch.
	primary bool
}

// installedPackages enumerates every package already present in the scope: the
// primary package (project scope, when <root>/app.manifest.toml exists) plus
// every guest under manifests/. A guest whose metadata is missing or unreadable
// is skipped rather than failing the whole install — a half-written earlier
// install must not wedge every later one.
func installedPackages(lay layout, scope Scope) ([]installedPackage, error) {
	var packages []installedPackage

	if scope == ScopeProject {
		if primary, ok := readPrimary(lay.root); ok {
			packages = append(packages, primary)
		}
	}

	entries, err := os.ReadDir(lay.manifests)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return packages, nil
		}

		return nil, fmt.Errorf("reading install state %s: %w", lay.manifests, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		guest, ok := readGuest(lay.manifests, entry.Name())
		if ok {
			packages = append(packages, guest)
		}
	}

	return packages, nil
}

// findInstalled returns the installed package with the given name, if any.
func findInstalled(packages []installedPackage, name string) (installedPackage, bool) {
	for _, p := range packages {
		if p.name == name {
			return p, true
		}
	}

	var zero installedPackage

	return zero, false
}

// readPrimary reads the project's own package from the install-root. The
// manifest is optional (a bare deploy directory has none); when present its lock
// beside it is read too.
func readPrimary(root string) (installedPackage, bool) {
	var zero installedPackage

	//nolint:gosec // Reads tt's own install-state files.
	manBytes, err := os.ReadFile(filepath.Join(root, primaryManifestFile))
	if err != nil {
		return zero, false
	}

	man, _, err := manifest.ParseManifest(manBytes)
	if err != nil {
		return zero, false
	}

	pkg := installedPackage{
		name:     man.Package.Name,
		manifest: man,
		lock:     readLockFile(filepath.Join(root, primaryLockFile)),
		version:  "",
		primary:  true,
	}

	return pkg, true
}

// readGuest reads one installed guest's metadata from manifests/<name>/.
func readGuest(manifestsDir, name string) (installedPackage, bool) {
	var zero installedPackage

	dir := filepath.Join(manifestsDir, name)

	//nolint:gosec // Reads tt's own install-state files.
	manBytes, err := os.ReadFile(filepath.Join(dir, metaManifestFile))
	if err != nil {
		return zero, false
	}

	man, _, err := manifest.ParseManifest(manBytes)
	if err != nil {
		return zero, false
	}

	pkg := installedPackage{
		name:     man.Package.Name,
		manifest: man,
		lock:     readLockFile(filepath.Join(dir, metaLockFile)),
		version:  readVersionFile(filepath.Join(dir, metaVersionFile)),
		primary:  false,
	}

	return pkg, true
}

// readLockFile parses a lock file, returning nil when it is absent or unreadable
// — a guest may predate a lock-writing tt, and the primary package need not have
// a lock at all.
func readLockFile(path string) *manifest.Lock {
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

// writeMetadata records a freshly installed guest's metadata under
// manifests/<pkg>/: the manifest, the lock and the version. This is the
// inventory list/uninstall read and the ledger dependency refcounting stands
// on — a dependency is owned by every package whose lock lists it.
func writeMetadata(lay layout, header *Header) error {
	dir := filepath.Join(lay.manifests, header.Manifest.Package.Name)

	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating metadata directory: %w", err)
	}

	lockBytes, err := header.Lock.Marshal()
	if err != nil {
		return fmt.Errorf("serializing lock metadata: %w", err)
	}

	files := []struct {
		name string
		data []byte
	}{
		{metaManifestFile, header.ManifestBytes},
		{metaLockFile, lockBytes},
		{metaVersionFile, []byte(header.Version + "\n")},
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
