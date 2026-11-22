package pack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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
	"github.com/tarantool/tt/v3/cli/manifest/rockindex"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// Formats of what tt package pack makes, the values of --format.
const (
	// FormatTt is the .tt archive of the package: an application to install.
	FormatTt = "tt"
	// FormatRock is the rock of the package, for other projects to depend
	// on: the binary rock of the platform it was built for, or the .all rock
	// of a package with no native file, and its rockspec beside it.
	FormatRock = "rock"
	// FormatRockSrc is the source rock of the package, for a platform with
	// no binary rock: the rockspec of the rock and the files of the project,
	// which tt builds with its manifest, and LuaRocks by the build table of
	// the rockspec (releaseBuild).
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
	// sourcePath is the source of a path dependency (manifest.Dependency).
	sourcePath = "path"
	// tarantoolRock is the name a rockspec depends on Tarantool by.
	tarantoolRock = "tarantool"
	// darwinOS is the GOOS of macOS, and luaRocksDarwin the name LuaRocks
	// gives it, in the arch of a rock and in its supported_platforms.
	darwinOS       = "darwin"
	luaRocksDarwin = "macosx"
)

// nativeModuleExts end the names of the native modules Tarantool loads: .so,
// and .dylib on darwin.
var nativeModuleExts = []string{".so", ".dylib"} //nolint:gochecknoglobals // A constant list.

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
	// errCrossDeps reports a .tt archive with its dependencies of a build
	// for another platform: the runtime and the dependencies it carries are
	// those of the machine.
	errCrossDeps = errors.New("a .tt archive with its dependencies is packed for the " +
		"machine tt runs on alone")
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
		if opts.cross() && !opts.WithoutDeps {
			return stateErrorf("%w, %s: its _runtime/ is the Tarantool and the tt of this "+
				"machine, and its .rocks/ the dependencies built for it; pack the archive for "+
				"%s with --without-deps, whose dependencies are installed where it is "+
				"installed, or pack --format rock", errCrossDeps, native.Host(),
				opts.Build.Target)
		}

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

// cross reports whether the pack builds the package for another platform
// than the machine tt runs on (build.Options.Target).
func (opts Options) cross() bool {
	target := opts.Build.Target

	return target != (native.Platform{OS: "", Arch: ""}) && target != native.Host()
}

// makes reports whether the pack makes format.
func (opts Options) makes(format string) bool {
	if len(opts.Formats) == 0 {
		return format == FormatTt
	}

	return slices.Contains(opts.Formats, format)
}

// srcRockOnly reports whether the source rock is all the pack makes.
func (opts Options) srcRockOnly() bool {
	return opts.makes(FormatRockSrc) && !opts.makes(FormatRock) && !opts.makes(FormatTt)
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
// directory as a rock server, the rocks already in it staying: the LuaRocks
// manifests and the tt-rocks-index.json (rockindex.MakeServer).
//
// The rock is the stage of the product the build laid out, the rock the build
// installed into .rocks/, but for other projects: it leaves _rocks.lua out,
// the rocks being theirs to resolve, and its rockspec declares the ranges of
// the manifest, as rock, which declareRock made before the build, has them.
// It is the binary rock of the platform the build was for - the host, or the
// target of a build for another platform - when the stage holds a native
// file or a file in lib/, and an .all rock otherwise, so the arch of a rock
// is never a choice: a module built for the host in an .all rock would
// install on any machine and fail there.
//
// The source rock holds the same rockspec and the files of the project
// (writeSrcRock): the rockspec is one per version, whatever rocks of it a
// server has. Its build table says how LuaRocks builds the source rock, of
// the files the source rock holds (releaseBuild), so it is the same whether
// the pack makes the source rock or not.
func packRocks(
	ctx context.Context, opts Options, built *build.Result, rock rockDecl,
) (rockResult, error) {
	carried, err := sourceFiles(ctx, opts.ProjectDir, built.Manifest.Package.LicenseFiles)
	if err != nil {
		return rockResult{}, err
	}

	rel := releaseRock(built, rock, opts.Revision)

	plan, err := releaseBuild(built, rel, carried)
	if err != nil {
		return rockResult{}, err
	}

	rel.Build = plan.build

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
		result.Rock, err = packBinaryRock(ctx, opts, built, dir, spec)
		if err != nil {
			return rockResult{}, err
		}
	}

	if opts.makes(FormatRockSrc) {
		result.SrcRock = filepath.Join(dir, base+"."+srcRockArch+rockExt)

		untracked := untrackedSources(ctx, opts.ProjectDir,
			componentPaths(built.Manifest, built.Product))

		warnings := plan.warnings(untracked)
		if warning := untrackedWarning(untracked); warning != "" {
			warnings = append(warnings, warning)
		}

		if opts.Warn != nil {
			for _, warning := range warnings {
				opts.Warn(warning)
			}
		}

		err = writeSrcRock(result.SrcRock, opts.ProjectDir,
			srcDirName(rel.SourceURL, rel.Name), base+rockspecExt, spec.RawSource,
			built.Version.SemVer, carried)
		if err != nil {
			return rockResult{}, err
		}
	}

	err = os.WriteFile(result.Rockspec, spec.RawSource, filePerm)
	if err != nil {
		return rockResult{}, fmt.Errorf("writing the rockspec: %w", err)
	}

	_, err = rockindex.MakeServer(ctx, dir, rockindex.ServerOptions{
		HTML: false, IfIndexed: false, Warn: opts.Warn,
	})
	if err != nil {
		return rockResult{}, err
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

// packBinaryRock packs the stage of built as the rock of spec into dir and
// returns its path: the binary rock of the platform the build was for, or
// the .all rock (rockArch), with the license files of the package in its
// doc/ and without the _rocks.lua of the stage, if the build generated one
// (rockFiles), and its native files without their debug information when
// the pack strips it (strippedStage).
func packBinaryRock(ctx context.Context,
	opts Options, built *build.Result, dir string, spec *luarocks.Rockspec,
) (string, error) {
	man, stage, rocksLua := built.Manifest, built.Stage, built.RocksLua

	arch, err := rockArch(stage, built.Target)
	if err != nil {
		return "", err
	}

	// _rocks.lua is left out by its path in the stage, as strippedStage copies
	// the stage elsewhere.
	leaveOut := ""
	if rocksLua != "" {
		leaveOut, err = filepath.Rel(stage, rocksLua)
		if err != nil {
			return "", fmt.Errorf("leaving %s out of the rock: %w", rocksLua, err)
		}
	}

	// The temporary tree of PackStaged goes under _build/, as the stage of the
	// archive does, rather than into the system's temporary directory.
	temp := filepath.Join(opts.ProjectDir, buildDirName, packSubDir)

	err = os.MkdirAll(temp, dirPerm)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", temp, err)
	}

	if opts.stripsDebugInfo() {
		stripped, cleanup, err := strippedStage(ctx, temp, stage, built.Target)
		if err != nil {
			return "", err
		}

		defer cleanup()

		stage = stripped
	}

	files, cleanup, err := rockFiles(temp, stage, opts.ProjectDir, man.Package.LicenseFiles,
		leaveOut)
	if err != nil {
		return "", err
	}

	defer cleanup()

	// PackStaged packs into a directory of its own: _build/pack is also the
	// default output directory, where the rock written from the packed one
	// lands under the same name, and the removal of the packed one would
	// remove it.
	packDir, err := os.MkdirTemp(temp, "rock-")
	if err != nil {
		return "", fmt.Errorf("creating a directory under %s: %w", temp, err)
	}

	defer func() { _ = os.RemoveAll(packDir) }()

	name := spec.Package + "-" + spec.Version + "." + arch + rockExt
	packed := filepath.Join(packDir, name)

	err = client.PackStaged(spec, files, packed, client.PackStagedOpts{TempDir: temp})
	if err != nil {
		return "", fmt.Errorf("packing the rock: %w", err)
	}

	// The entries go-luarocks writes carry no time; they get the one of the
	// entries of the source rock.
	data, err := restampZip(packed)
	if err != nil {
		return "", err
	}

	rock := filepath.Join(dir, name)

	err = writeAtomically(rock, data)
	if err != nil {
		return "", err
	}

	return rock, nil
}

// rockFiles lays out the files of the binary rock in a directory and returns
// it, with the cleanup that removes it: the stage of the product, and the
// license files of the package in doc/ (rocktree.StageDoc), each at its path
// in the project, where LuaRocks keeps the documents of a rock - a project
// installing the rock has them in the install dir of the rock, as it has
// those of the rocks LuaRocks builds.
//
// The rock leaves out leaveOut, a file of the stage by its path relative to
// the stage, if not empty: _rocks.lua (build.Result.RocksLua), the rocks the
// application was built with, which describe no project installing the rock -
// such a project resolves its own.
//
// The stage is copied into a directory of its own under temp for either
// (rocktree.WithDocs); a package with no license files and no file to leave
// out is packed from its stage as it is.
func rockFiles(
	temp, stage, projectDir string, licenseFiles []string, leaveOut string,
) (string, func(), error) {
	if len(licenseFiles) == 0 && leaveOut == "" {
		return stage, func() {}, nil
	}

	return rocktree.WithDocs(temp, stage, func(doc string) error {
		if leaveOut != "" {
			// WithDocs gives doc/ of the copy of the stage, at its root.
			dst := filepath.Join(filepath.Dir(doc), leaveOut)

			removeErr := os.Remove(dst)
			if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				return fmt.Errorf("leaving %s out of the rock: %w", leaveOut, removeErr)
			}

			// A namespace that held nothing else is left out with it.
			_ = os.Remove(filepath.Dir(dst))
		}

		for _, entry := range licenseFiles {
			err := stageEntry(doc, projectDir, entry, "license_files")
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// rockArch is the LuaRocks arch of the rock of stage, which a build for
// target made (build.Result.Target): that of target (rockArchOf) when it
// holds a native file, ELF or Mach-O, which has to be one of target
// (checkNativeFiles), or a file in lib/ named as a native module is
// (holdsModules); all otherwise, whatever else lib/ holds - a .wasm that a
// build put among the modules runs anywhere, as the .tt archive of the same
// files says (native.Files).
func rockArch(stage string, target native.Platform) (string, error) {
	natives, err := native.Files(stage)
	if err != nil {
		return "", fmt.Errorf("finding the native files of the rock: %w", err)
	}

	err = checkNativeFiles(natives, target)
	if err != nil {
		return "", err
	}

	if len(natives) > 0 {
		return rockArchOf(target), nil
	}

	hasModules, err := holdsModules(filepath.Join(stage, stageLib))
	if err != nil {
		return "", err
	}

	if hasModules {
		return rockArchOf(target), nil
	}

	return rockArchAll, nil
}

// rockArchOf is the LuaRocks arch of the binary rocks of platform, as
// LuaRocks names the arch of a machine (cfg.arch): <os>-<cpu>, macosx for
// darwin, x86_64 and aarch64 for amd64 and arm64. It is remote.HostArch for
// the machine tt runs on, the arch the rocks of a project are installed of.
func rockArchOf(platform native.Platform) string {
	if platform == native.Host() {
		return remote.HostArch()
	}

	osName := platform.OS
	if osName == darwinOS {
		osName = luaRocksDarwin
	}

	cpu := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[platform.Arch]
	if cpu == "" {
		cpu = platform.Arch
	}

	return osName + "-" + cpu
}

// holdsModules reports whether the directory dir holds a file named as a
// native module Tarantool loads is named, a .so or a .dylib, at any depth,
// whatever its content: a build put it among the modules of the package for
// the host, which tt cannot tell of a file that is neither ELF nor Mach-O.
// Any other file there - a .wasm, Lua - runs anywhere and makes no rock of
// the host. A missing directory holds none.
func holdsModules(dir string) (bool, error) {
	found := false

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() && slices.ContainsFunc(nativeModuleExts, func(ext string) bool {
			return strings.HasSuffix(path, ext)
		}) {
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

	ver := built.Version

	return rocktree.Release{
		Name:         rock.name,
		Version:      ver.SemVer,
		Revision:     revision,
		Summary:      man.Package.Description,
		Homepage:     man.Package.Repository,
		License:      man.Package.License,
		Maintainer:   strings.Join(man.Package.Authors, ", "),
		SourceURL:    sourceURL(man.Package.Repository),
		SourceTag:    ver.Tag,
		SourceCommit: ver.CommitHash,
		Uncommitted:  ver.Dirty,
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

	out := make(map[string]string, len(declared))

	var warnings []string

	for _, name := range slices.Sorted(maps.Keys(declared)) {
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
		case darwinOS:
			name = luaRocksDarwin
		default:
			return nil
		}

		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}

	return out
}
