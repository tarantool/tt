package resolve

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/srctree"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// The files of a project the resolver looks for in the directory of a path
// dependency.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
)

// PathManifest is a path dependency whose directory holds app.manifest.toml: a
// neighbour project tt builds with its own manifest pipeline, as the rock of
// the package the manifest describes. The manifest is the source of truth: a
// rockspec beside it is ignored, and so is the neighbour's own lock - the
// consumer resolves the neighbour's dependencies itself, as Cargo does for a
// path dependency.
type PathManifest struct {
	// Dir is the directory of the dependency.
	Dir string
	// Manifest is its manifest, parsed and validated.
	Manifest *manifest.Manifest
	// Product names the product a consumer builds: the one released as the
	// rock the dependency is named after (manifest.ProductRock), the default
	// one for the package name.
	Product string
	// Warnings are the warnings of parsing and validating the manifest, each
	// naming the dependency.
	Warnings []string
}

// IsPathManifest reports whether dir, the directory of a path dependency,
// holds app.manifest.toml.
func IsPathManifest(dir string) (bool, error) {
	info, err := os.Stat(filepath.Join(dir, manifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("looking for %s in %s: %w", manifestFileName, dir, err)
	}

	return info.Mode().IsRegular(), nil
}

// LoadPathManifest reads the manifest of the path dependency name from dir,
// its directory, which holds one (IsPathManifest). The manifest has to
// describe the package name: the dependency is installed as the rock of that
// package, so a dependency named otherwise is ErrPathPackageName.
func LoadPathManifest(name, dir string) (*PathManifest, error) {
	path := filepath.Join(dir, manifestFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("path dependency %q: reading %s: %w", name, path, err)
	}

	man, warnings, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("path dependency %q: parsing %s: %w", name, path, err)
	}

	validationWarnings, err := man.Validate()
	if err != nil {
		return nil, fmt.Errorf("path dependency %q: invalid manifest %s: %w", name, path, err)
	}

	if _, _, ok := man.DefaultProduct(); !ok {
		// Validate refuses such a manifest; this keeps an unvalidated one from
		// building nothing in silence.
		return nil, fmt.Errorf("path dependency %q: %s: %w", name, path, errNoDefaultProduct)
	}

	// The dependency is the rock of one product of the package: the default
	// one by the package name, another by the rock it names.
	product, ok := man.RockProduct(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q is the package %q (%s); name the dependency %s",
			ErrPathPackageName, name, man.Package.Name, path, rockNames(man))
	}

	loaded := &PathManifest{Dir: dir, Manifest: man, Product: product, Warnings: nil}

	for _, warning := range append(warnings, validationWarnings...) {
		loaded.Warnings = append(loaded.Warnings,
			fmt.Sprintf("path dependency %q: %s", name, warning))
	}

	return loaded, nil
}

// rockNames lists the rocks the products of man are released as, quoted, for
// a message: "a", "a" or "b".
func rockNames(man *manifest.Manifest) string {
	var names []string

	for _, product := range slices.Sorted(maps.Keys(man.EffectiveProducts())) {
		if rock := man.ProductRock(product); rock != "" {
			names = append(names, strconv.Quote(rock))
		}
	}

	if len(names) <= 1 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// Components returns the components of the product of the dependency.
func (p *PathManifest) Components() []string {
	return p.Manifest.EffectiveProducts()[p.Product].Components
}

// Version derives the version of the dependency from its directory, as tt
// package build in that directory does (version.Derive), and returns it with
// the version of its rock (rocktree.RockVersion), which is what the lock
// records and the rock is installed at.
func (p *PathManifest) Version() (version.Version, string, error) {
	ver, err := version.Derive(p.Dir)
	if err != nil {
		return version.Version{}, "", fmt.Errorf("path dependency %q: deriving the version: %w",
			p.Manifest.Package.Name, err)
	}

	ver.Flavor = p.Manifest.Platform.Tarantool.EffectiveFlavor()

	rockVersion, err := rocktree.RockVersion(ver.SemVer)
	if err != nil {
		return version.Version{}, "", fmt.Errorf("path dependency %q: %w",
			p.Manifest.Package.Name, err)
	}

	return ver, rockVersion, nil
}

// Registries maps the dependencies of the product (Product) that name a
// registry - globally or in a component - to that registry, a directory
// resolved against the dependency's own directory, as its own build resolves
// it. A consumer installs those rocks from there, exactly as the neighbour
// would.
func (p *PathManifest) Registries() (map[string]string, error) {
	components := p.Components()

	declared := append(make([]map[string]manifest.Dependency, 0, 1+len(components)),
		p.Manifest.Dependencies)

	for _, component := range components {
		declared = append(declared, p.Manifest.Components[component].Dependencies)
	}

	registries := map[string]string{}

	for _, table := range declared {
		for name, dependency := range table {
			if dependency.Registry == "" {
				continue
			}

			if _, seen := registries[name]; seen {
				continue
			}

			registry, err := rocks.DependencyRegistry(dependency.Registry, p.Dir)
			if err != nil {
				return nil, fmt.Errorf("path dependency %q: registry of %q: %w",
					p.Manifest.Package.Name, name, err)
			}

			registries[name] = registry
		}
	}

	return registries, nil
}

// requirements are the direct requirements of the dependency, as its consumer
// resolves them: the effective dependencies of its product (Product), a path
// one rebased from the dependency's directory onto projectDir, the
// consumer's, and a registry one resolved against the dependency's directory.
// [dev_dependencies] are the neighbour's own business and are left out.
func (p *PathManifest) requirements(projectDir string) ([]depReq, error) {
	name := p.Manifest.Package.Name

	reqs, err := effectiveDeps(p.Manifest, p.Manifest.EffectiveProducts()[p.Product])
	if err != nil {
		return nil, fmt.Errorf("path dependency %q: %w", name, err)
	}

	for idx := range reqs {
		req := &reqs[idx]

		if req.registry != "" {
			req.registry, err = rocks.DependencyRegistry(req.registry, p.Dir)
			if err != nil {
				return nil, fmt.Errorf("path dependency %q: registry of %q: %w",
					name, req.name, err)
			}
		}

		if req.source == manifestSourcePath {
			req.path, err = rebasePath(projectDir, filepath.Join(p.Dir, req.path))
			if err != nil {
				return nil, fmt.Errorf("path dependency %q: path of %q: %w",
					name, req.name, err)
			}
		}
	}

	return reqs, nil
}

// rebasePath writes dir, a directory, relative to projectDir, slash-separated
// as the lock is: a path dependency of a path dependency is written relative
// to that dependency, and the consumer finds it relative to itself.
func rebasePath(projectDir, dir string) (string, error) {
	base, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", projectDir, err)
	}

	target, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, err)
	}

	rel, err := filepath.Rel(base, target)
	if err != nil {
		return filepath.ToSlash(target), nil //nolint:nilerr // Another volume: absolute.
	}

	return filepath.ToSlash(rel), nil
}

// rockspecsBeside lists the rockspecs at the top of dir, which a manifest
// beside them overrides.
func rockspecsBeside(dir string) ([]string, error) {
	specs, err := filepath.Glob(filepath.Join(dir, "*.rockspec"))
	if err != nil {
		return nil, fmt.Errorf("looking for rockspecs in %s: %w", dir, err)
	}

	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, filepath.Base(spec))
	}

	return names, nil
}

// manifestContentHash is contentHash of a path dependency that holds a
// manifest: the same framing over the files a build of the dependency reads,
// so that neither its own build nor a consumer's build of it makes a lock of
// the consumer stale. What is left out is what such a build writes or what is
// no source of it: .git, _build, .rocks and _runtime wherever they are, the
// dependency's own app.manifest.lock, which a consumer ignores, and whatever
// the .gitignore files of the dependency ignore, each for its subtree, as git
// reads them (srctree.Walk). The .gitignore files themselves are content: a
// changed rule changes what is hashed. Symbolic links are skipped, as
// contentHash skips them, and a symlinked root is resolved first.
func manifestContentHash(dir string) (string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, err)
	}

	var files []fileEntry

	err = srctree.Walk(root, nil, nil, skipManifestContent, func(
		full string, path []string, entry fs.DirEntry,
	) error {
		if !entry.Type().IsRegular() {
			return nil
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("stat %s: %w", full, infoErr)
		}

		files = append(files, fileEntry{
			rel:  strings.Join(path, "/"),
			exec: info.Mode().Perm()&0o111 != 0,
		})

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking %s: %w", root, err)
	}

	return hashEntries(root, files)
}

// skipManifestContent names what manifestContentHash leaves out before the
// ignore rules are asked.
func skipManifestContent(path []string, _ fs.DirEntry) bool {
	switch path[len(path)-1] {
	case ".git", "_build", ".rocks", "_runtime":
		return true
	case lockFileName:
		return len(path) == 1
	default:
		return false
	}
}

// pathContentHash is the content hash of the directory of a path dependency,
// as the lock records it: manifestContentHash when the directory holds a
// manifest, contentHash of every file otherwise. The resolver and IsStale
// both hash through it, so a lock they write is fresh for them.
func pathContentHash(dir string) (string, error) {
	isManifest, err := IsPathManifest(dir)
	if err != nil {
		return "", err
	}

	if isManifest {
		return manifestContentHash(dir)
	}

	return contentHash(dir)
}
