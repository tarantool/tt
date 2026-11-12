package pack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// Formats of what tt package pack makes, the values of --format.
const (
	// FormatTt is the .tt archive of the package: an application to install.
	FormatTt = "tt"
	// FormatRock is the rock of the package, for other projects to depend
	// on: the binary rock of the host, or the .all rock of a package with no
	// native file, and its rockspec beside it.
	FormatRock = "rock"
	// FormatRockSrc is the source rock of the package, for a platform with
	// no binary rock: the rockspec of the rock and the files of the project,
	// which tt builds with its manifest.
	FormatRockSrc = "rock-src"
)

// formats are the values of --format.
var formats = []string{FormatTt, FormatRock, FormatRockSrc}

const (
	// rockExt and rockspecExt end the names of a rock and of a rockspec.
	rockExt     = ".rock"
	rockspecExt = ".rockspec"
	// rockArchAll is the arch of a rock that holds no native file.
	rockArchAll = "all"
	// stageLib is the directory of the stage of the product its native
	// modules are in (rocktree.StageLib).
	stageLib = "lib"
	// rockDocDir is the directory of a binary rock the license files of the
	// package go to, unpacked into the install dir of the rock with the rest
	// of its documents.
	rockDocDir = "doc"
	// sourcePath is the source of a path dependency (manifest.Dependency).
	sourcePath = "path"
	// tarantoolRock is the name a rockspec depends on Tarantool by.
	tarantoolRock = "tarantool"
)

var (
	// errFormat reports a --format tt package pack does not know.
	errFormat = errors.New("unknown format")
	// errArchiveOnly reports an option of the .tt archive given to a pack
	// that makes none.
	errArchiveOnly = errors.New("only the .tt archive has the option")
	// errPathDependency reports a path dependency of a package packed as a
	// rock: the projects installing the rock have no such directory.
	errPathDependency = errors.New("a rock cannot depend on a path dependency")
	// errNoRock reports a product packed as a rock that has no rock of its
	// own (manifest.ProductRock).
	errNoRock = errors.New("the product has no rock of its own")
)

// checkFormats reports a format pack does not make, or options of the .tt
// archive given to a pack that does not make it. An empty list is the .tt
// archive.
func checkFormats(opts Options) error {
	for _, format := range opts.Formats {
		if !slices.Contains(formats, format) {
			return stateErrorf("%w %q: want %s", errFormat, format, strings.Join(formats, ", "))
		}
	}

	if opts.makes(FormatTt) {
		return nil
	}

	switch {
	case opts.WithoutDeps:
		return stateErrorf("%w: --without-deps", errArchiveOnly)
	case opts.Signer != nil:
		return stateErrorf("%w: signing", errArchiveOnly)
	}

	return nil
}

// makes reports whether the pack makes format.
func (opts Options) makes(format string) bool {
	if len(opts.Formats) == 0 {
		return format == FormatTt
	}

	return slices.Contains(opts.Formats, format)
}

// makesRock reports whether the pack makes the rock, the source rock or both.
func (opts Options) makesRock() bool {
	return opts.makes(FormatRock) || opts.makes(FormatRockSrc)
}

// rockDecl is the rock a product is released as, as the manifest declares
// it: what the rockspec says of it but the version, which the build derives.
type rockDecl struct {
	// name is the name of the rock (manifest.ProductRock).
	name string
	// dependencies are the constraints of its dependencies by name, in
	// LuaRocks syntax (releaseDependencies).
	dependencies map[string]string
}

// declareRock declares the rock product of man is released as. A product
// with no rock of its own and a path dependency fail it; it returns warnings
// for what the rockspec cannot say.
func declareRock(man *manifest.Manifest, product string) (rockDecl, []string, error) {
	name := man.ProductRock(product)
	if name == "" {
		return rockDecl{}, nil, stateErrorf("%w: %s is not the default product; "+
			"name its rock with [products.%s].rock", errNoRock, product, product)
	}

	dependencies, warnings, err := releaseDependencies(man, product)
	if err != nil {
		return rockDecl{}, nil, err
	}

	return rockDecl{name: name, dependencies: dependencies}, warnings, nil
}

// rockCheck is the check of the build (build.Options.Check) of a pack that
// makes a rock: it declares the rock of the product into rock, so a product
// no rock can be made of fails before anything is built. A pack that makes
// no rock checks nothing.
func rockCheck(opts Options, rock *rockDecl) func(*manifest.Manifest, string) error {
	if !opts.makesRock() {
		return nil
	}

	return func(man *manifest.Manifest, product string) error {
		decl, warnings, err := declareRock(man, product)
		if err != nil {
			return err
		}

		if opts.Warn != nil {
			for _, warning := range warnings {
				opts.Warn(warning)
			}
		}

		*rock = decl

		return nil
	}
}

// rockResult is what packRocks made.
type rockResult struct {
	// Rock, SrcRock and Rockspec are the absolute paths of the rock, of the
	// source rock and of their rockspec; a rock not asked for is empty.
	Rock     string
	SrcRock  string
	Rockspec string
}

// packRocks packs the rock of the package, its source rock or both into the
// output directory, writes their rockspec beside them and indexes the
// directory as a rock server, the rocks already in it staying.
//
// The rock is the stage of the product the build laid out, the rock the build
// installed into .rocks/, but for other projects: its rockspec declares the
// ranges of the manifest, as rock, which declareRock made before the build,
// has them. It is the binary rock of the host when the stage holds a native
// file or a file in lib/, and an .all rock otherwise, so the arch of a rock
// is never a choice: a module built for the host in an .all rock would
// install on any machine and fail there.
//
// The source rock holds the same rockspec and the files of the project
// (writeSrcRock): the rockspec is one per version, whatever rocks of it a
// server has.
func packRocks(
	ctx context.Context, opts Options, built *build.Result, rock rockDecl,
) (rockResult, error) {
	rel := releaseRock(built, rock, opts.Revision)

	spec, err := rocktree.ReleaseRockspec(rel)
	if err != nil {
		return rockResult{}, stateErrorf("%w", err)
	}

	dir := outputDir(opts)

	err = os.MkdirAll(dir, dirPerm)
	if err != nil {
		return rockResult{}, fmt.Errorf("creating %s: %w", dir, err)
	}

	base := spec.Package + "-" + spec.Version
	result := rockResult{Rock: "", SrcRock: "", Rockspec: filepath.Join(dir, base+rockspecExt)}

	if opts.makes(FormatRock) {
		result.Rock, err = packBinaryRock(opts, built.Manifest, built.Stage, dir, spec)
		if err != nil {
			return rockResult{}, err
		}
	}

	if opts.makes(FormatRockSrc) {
		result.SrcRock = filepath.Join(dir, base+"."+srcRockArch+rockExt)

		warning := untrackedWarning(untrackedSources(ctx, opts.ProjectDir,
			componentPaths(built.Manifest, built.Product)))
		if warning != "" && opts.Warn != nil {
			opts.Warn(warning)
		}

		err = writeSrcRock(ctx, result.SrcRock, opts.ProjectDir,
			srcDirName(rel.SourceURL, rel.Name), base+rockspecExt, spec.RawSource,
			built.Version.SemVer)
		if err != nil {
			return rockResult{}, err
		}
	}

	err = os.WriteFile(result.Rockspec, spec.RawSource, filePerm)
	if err != nil {
		return rockResult{}, fmt.Errorf("writing the rockspec: %w", err)
	}

	err = remote.MakeManifest(ctx, dir, remote.MakeManifestOpts{
		LuaVersions: nil, Index: false, Zip: false, Warn: opts.Warn,
	})
	if err != nil {
		return rockResult{}, fmt.Errorf("indexing %s: %w", dir, err)
	}

	return result, nil
}

// componentPaths lists the paths of the components of product, in order and
// without repeats.
func componentPaths(man *manifest.Manifest, product string) []string {
	var paths []string

	for _, name := range man.EffectiveProducts()[product].Components {
		path := man.Components[name].Path
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}

	return paths
}

// packBinaryRock packs stage as the rock of spec into dir and returns its
// path: the binary rock of the host, or the .all rock (rockArch), with the
// license files of the package in its doc/ (rockFiles).
func packBinaryRock(
	opts Options, man *manifest.Manifest, stage, dir string, spec *luarocks.Rockspec,
) (string, error) {
	arch, err := rockArch(stage)
	if err != nil {
		return "", err
	}

	// The temporary tree of PackStaged goes under _build/, as the stage of the
	// archive does, rather than into the system's temporary directory.
	temp := filepath.Join(opts.ProjectDir, buildDirName, packSubDir)

	err = os.MkdirAll(temp, dirPerm)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", temp, err)
	}

	files, cleanup, err := rockFiles(temp, stage, opts.ProjectDir, man.Package.LicenseFiles)
	if err != nil {
		return "", err
	}

	defer cleanup()

	rock := filepath.Join(dir, spec.Package+"-"+spec.Version+"."+arch+rockExt)

	err = client.PackStaged(spec, files, rock, client.PackStagedOpts{TempDir: temp})
	if err != nil {
		return "", fmt.Errorf("packing the rock: %w", err)
	}

	return rock, nil
}

// rockFiles lays out the files of the binary rock in a directory and returns
// it, with the cleanup that removes it: the stage of the product, and the
// license files of the package in doc/, each at its path in the project, where
// LuaRocks keeps the documents of a rock - a project installing the rock has
// them in the install dir of the rock, as it has those of the rocks LuaRocks
// builds. doc/ is no directory of the stage, which holds lua/, lib/ and bin/
// alone, so it changes nothing of what the rock deploys, nor its arch.
//
// The license files are not the build's, so they do not go into the stage: the
// build removes from it what no component owns, and a later build or install
// of the stage would carry what it does not remove. The stage is copied
// instead into a directory of its own under temp, the license files beside it.
// A package with no license files is packed from its stage as it is.
func rockFiles(temp, stage, projectDir string, licenseFiles []string) (string, func(), error) {
	if len(licenseFiles) == 0 {
		return stage, func() {}, nil
	}

	dir, err := os.MkdirTemp(temp, "rock-")
	if err != nil {
		return "", nil, fmt.Errorf("creating the tree of the rock: %w", err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }

	err = mirrorTree(stage, dir)
	if err != nil {
		cleanup()

		return "", nil, fmt.Errorf("copying the stage of the rock: %w", err)
	}

	for _, entry := range licenseFiles {
		err = stageEntry(filepath.Join(dir, rockDocDir), projectDir, entry, "license_files")
		if err != nil {
			cleanup()

			return "", nil, err
		}
	}

	return dir, cleanup, nil
}

// mirrorTree copies the tree src into the directory dst as PackStaged reads
// it: a file with the permission bits it has, which the rock records for a
// program, and a symlink as the link it is, which PackStaged refuses when it
// leads out of the tree. A missing src is an empty tree, and what is neither
// a file, a directory nor a symlink is left out.
func mirrorTree(src, dst string) error {
	_, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	//nolint:wrapcheck // Every error the walk yields already names the entry it failed on.
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}

		target := filepath.Join(dst, rel)

		switch {
		case entry.IsDir():
			return os.MkdirAll(target, dirPerm)
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}

			//nolint:gosec // dst is a fresh directory of the pack, src the stage of the build.
			return os.Symlink(link, target)
		case entry.Type().IsRegular():
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("copying %s: %w", path, err)
			}

			return copyFileAs(path, target, info.Mode().Perm())
		default:
			return nil
		}
	})
}

// rockArch is the LuaRocks arch of the rock of stage: the host's when it holds
// a native file, which has to be one of the host (checkNativeFiles), or any
// file in lib/; all otherwise.
func rockArch(stage string) (string, error) {
	natives, err := native.Files(stage)
	if err != nil {
		return "", fmt.Errorf("finding the native files of the rock: %w", err)
	}

	err = checkNativeFiles(natives, native.Host())
	if err != nil {
		return "", err
	}

	if len(natives) > 0 {
		return remote.HostArch(), nil
	}

	hasLib, err := holdsFiles(filepath.Join(stage, stageLib))
	if err != nil {
		return "", err
	}

	if hasLib {
		return remote.HostArch(), nil
	}

	return rockArchAll, nil
}

// holdsFiles reports whether the directory dir holds a file, at any depth. A
// missing directory holds none.
func holdsFiles(dir string) (bool, error) {
	found := false

	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			found = true

			return fs.SkipAll
		}

		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("reading %s: %w", dir, err)
	}

	return found, nil
}

// releaseRock describes the rock the build is released as: rock, as the
// manifest declared it before the build, at the version the build derived.
func releaseRock(built *build.Result, rock rockDecl, revision int) rocktree.Release {
	if revision == 0 {
		revision = 1
	}

	man := built.Manifest

	source := sourceURL(man.Package.Repository)
	tag := ""

	if source != "" {
		tag = built.Version.Tag
	}

	return rocktree.Release{
		Name:         rock.name,
		Version:      built.Version.SemVer,
		Revision:     revision,
		Summary:      man.Package.Description,
		Homepage:     man.Package.Repository,
		License:      man.Package.License,
		Maintainer:   strings.Join(man.Package.Authors, ", "),
		SourceURL:    source,
		SourceTag:    tag,
		Platforms:    rockPlatforms(man.Platform.Platforms),
		Dependencies: rock.dependencies,
	}
}

// releaseDependencies collects the dependencies of the rock of product: those
// of [dependencies] and of the components of the product, as the build
// resolves them, each with the constraints of all its declarations together.
// A path dependency fails the pack, since no project installing the rock has
// the directory; a dependency from a registry of its own is kept, with a
// warning, since the rockspec cannot name the server.
func releaseDependencies(
	man *manifest.Manifest, product string,
) (map[string]string, []string, error) {
	declared := map[string][]manifest.Dependency{}

	add := func(dependencies map[string]manifest.Dependency) {
		for name, dependency := range dependencies {
			declared[name] = append(declared[name], dependency)
		}
	}

	add(man.Dependencies)

	for _, component := range man.EffectiveProducts()[product].Components {
		add(man.Components[component].Dependencies)
	}

	// The versions of Tarantool the package runs on are those a project
	// installing its rock has to run on: tt checks a dependency on tarantool
	// against [platform].tarantool of that project.
	if manifest.ConstraintExpr(man.Platform.Tarantool.Version) != "" {
		declared[tarantoolRock] = append(declared[tarantoolRock], manifest.Dependency{
			Source: "registry", Version: man.Platform.Tarantool.Version,
			Path: "", Registry: "", Kind: "",
		})
	}

	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}

	slices.Sort(names)

	out := make(map[string]string, len(declared))

	var warnings []string

	for _, name := range names {
		var constraints []luarocks.VersionConstraint

		registry := ""

		for _, dependency := range declared[name] {
			if dependency.Source == sourcePath {
				return nil, nil, stateErrorf("%w: %s is one of %s; release %s and depend "+
					"on a version of it", errPathDependency, name, man.Package.Name, name)
			}

			parsed, err := deps.ParseConstraints(manifest.ConstraintExpr(dependency.Version))
			if err != nil {
				return nil, nil, stateErrorf("dependency %s: %w", name, err)
			}

			constraints = append(constraints, parsed...)

			if dependency.Registry != "" {
				registry = dependency.Registry
			}
		}

		out[name] = formatConstraints(constraints)

		if registry != "" {
			warnings = append(warnings, fmt.Sprintf("the rock depends on %s from %s, which "+
				"its rockspec cannot name: a project installing the rock needs that server "+
				"among its own", name, registry))
		}
	}

	return out, warnings, nil
}

// formatConstraints renders constraints in LuaRocks syntax: ">= 1.0, < 2.0".
func formatConstraints(constraints []luarocks.VersionConstraint) string {
	parts := make([]string, 0, len(constraints))
	for _, constraint := range constraints {
		parts = append(parts, constraint.Op+" "+constraint.Version.Raw)
	}

	return strings.Join(parts, ", ")
}

// sourceURL is the git+ URL of the repository of the package, which the
// rockspec names as its source: an http(s) URL becomes git+http(s), a git+ or
// git:// one is kept, and any other names no source LuaRocks can fetch.
func sourceURL(repository string) string {
	switch {
	case strings.HasPrefix(repository, "https://"), strings.HasPrefix(repository, "http://"):
		return "git+" + repository
	case strings.HasPrefix(repository, "git+"), strings.HasPrefix(repository, "git://"):
		return repository
	default:
		return ""
	}
}

// rockPlatforms maps [platform].platforms to the LuaRocks platforms of the
// rockspec: linux-amd64 and linux-arm64 are linux, darwin-* macosx. "any",
// or no list, supports every platform and names none.
func rockPlatforms(platforms []string) []string {
	var out []string

	for _, platform := range platforms {
		goos, _, _ := strings.Cut(platform, "-")

		var name string

		switch goos {
		case "linux":
			name = "linux"
		case "darwin":
			name = "macosx"
		default:
			return nil
		}

		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}

	return out
}
