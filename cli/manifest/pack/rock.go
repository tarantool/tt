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
)

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
)

// checkFormats reports a format pack does not make, or options of the .tt
// archive given to a pack that does not make it. An empty list is the .tt
// archive.
func checkFormats(opts Options) error {
	for _, format := range opts.Formats {
		if format != FormatTt && format != FormatRock {
			return stateErrorf("%w %q: want %s or %s", errFormat, format, FormatTt, FormatRock)
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

// rockResult is what packRock made.
type rockResult struct {
	// Rock and Rockspec are the absolute paths of the rock and its rockspec.
	Rock     string
	Rockspec string
}

// packRock packs the stage of the product the build laid out as the rock of
// the package into the output directory, writes its rockspec beside it and
// indexes the directory as a rock server, the rocks already in it staying.
//
// The rock is the one the build installed into .rocks/, but for other
// projects: its rockspec declares the ranges of the manifest (releaseRock).
// It is the binary rock of the host when the stage holds a native file or a
// file in lib/, and an .all rock otherwise, so the arch of a rock is never
// a choice: a module built for the host in an .all rock would install on
// any machine and fail there.
func packRock(ctx context.Context, opts Options, built *build.Result) (rockResult, error) {
	rel, warnings, err := releaseRock(built, opts.Revision)
	if err != nil {
		return rockResult{}, err
	}

	for _, warning := range warnings {
		if opts.Warn != nil {
			opts.Warn(warning)
		}
	}

	spec, err := rocktree.ReleaseRockspec(rel)
	if err != nil {
		return rockResult{}, stateErrorf("%w", err)
	}

	arch, err := rockArch(built.Stage)
	if err != nil {
		return rockResult{}, err
	}

	dir := outputDir(opts)

	err = os.MkdirAll(dir, dirPerm)
	if err != nil {
		return rockResult{}, fmt.Errorf("creating %s: %w", dir, err)
	}

	// The temporary tree of PackStaged goes under _build/, as the stage of the
	// archive does, rather than into the system's temporary directory.
	temp := filepath.Join(opts.ProjectDir, buildDirName, packSubDir)

	err = os.MkdirAll(temp, dirPerm)
	if err != nil {
		return rockResult{}, fmt.Errorf("creating %s: %w", temp, err)
	}

	base := spec.Package + "-" + spec.Version
	rock := filepath.Join(dir, base+"."+arch+rockExt)

	err = client.PackStaged(spec, built.Stage, rock, client.PackStagedOpts{TempDir: temp})
	if err != nil {
		return rockResult{}, fmt.Errorf("packing the rock: %w", err)
	}

	rockspec := filepath.Join(dir, base+rockspecExt)

	err = os.WriteFile(rockspec, spec.RawSource, filePerm)
	if err != nil {
		return rockResult{}, fmt.Errorf("writing the rockspec: %w", err)
	}

	err = remote.MakeManifest(ctx, dir, remote.MakeManifestOpts{
		LuaVersions: nil, Index: false, Zip: false, Warn: opts.Warn,
	})
	if err != nil {
		return rockResult{}, fmt.Errorf("indexing %s: %w", dir, err)
	}

	return rockResult{Rock: rock, Rockspec: rockspec}, nil
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

// releaseRock describes the rock the build is released as. It returns
// warnings for what the rockspec cannot say.
func releaseRock(built *build.Result, revision int) (rocktree.Release, []string, error) {
	if revision == 0 {
		revision = 1
	}

	man := built.Manifest

	dependencies, warnings, err := releaseDependencies(man, built.Product)
	if err != nil {
		return rocktree.Release{}, nil, err
	}

	source := sourceURL(man.Package.Repository)
	tag := ""

	if source != "" {
		tag = built.Version.Tag
	}

	return rocktree.Release{
		Name:         man.Package.Name,
		Version:      built.Version.SemVer,
		Revision:     revision,
		Summary:      man.Package.Description,
		Homepage:     man.Package.Repository,
		License:      man.Package.License,
		Maintainer:   strings.Join(man.Package.Authors, ", "),
		SourceURL:    source,
		SourceTag:    tag,
		Platforms:    rockPlatforms(man.Platform.Platforms),
		Dependencies: dependencies,
	}, warnings, nil
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
