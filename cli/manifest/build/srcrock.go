package build

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/remote"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

const (
	// srcArch and allArch are the manifest arches of a source rock and of a
	// rock of no native file.
	srcArch = "src"
	allArch = "all"
	// srcRockFileMode and srcRockExecMode are the modes the files of an
	// unpacked source rock get: its own, but the executable bit, are not
	// kept.
	srcRockFileMode fs.FileMode = 0o644
	srcRockExecMode fs.FileMode = 0o755
)

var (
	// errSrcRockEntry reports an entry of a source rock that would land
	// outside the directory it is unpacked into.
	errSrcRockEntry = errors.New("entry escapes the directory of the source rock")
	// errSrcRockPackage reports a source rock that holds no tt package.
	errSrcRockPackage = errors.New("the source rock holds no app.manifest.toml")
	// errSrcRockLicense reports a license_files entry of the package of a
	// source rock that names no file of it.
	errSrcRockLicense = errors.New("the entry leads out of the package")
)

// srcRockFetcher finds and downloads source rocks: the slice of go-luarocks'
// *client.Rocks building a registry rock from its source needs. Its servers
// are those the rock is installed from, and Download writes into its working
// directory.
type srcRockFetcher interface {
	Search(
		ctx context.Context, pattern string, opts client.SearchOpts,
	) ([]client.SearchResult, error)
	Download(ctx context.Context, name string, opts client.DownloadOpts) (string, error)
}

// srcDirName is the directory of _build/ the registry rocks built from their
// source rocks are downloaded, unpacked and built in.
const srcDirName = "src"

// srcRocks builds the registry rocks of a closure that a server publishes no
// rock of for this host, but their source rock, when it is the source rock of
// a tt package: from its manifest, as a path dependency is built, into the
// consumer's tree. Install would take the source rock as any other and
// install its rockspec alone, since its build is done (build type none) for
// every rock of the version.
type srcRocks struct {
	// fetcher searches and downloads from the servers of the build, writing
	// into dir.
	fetcher srcRockFetcher
	// dir is _build/src/ of the consumer.
	dir string
	// tree is the consumer's rocks tree.
	tree string
	// builder builds the unpacked package.
	builder pathManifests
}

// install builds and installs the registry rock dep from its source rock, from
// servers, the ones the rock is installed from, if it is one to build so
// (needsSrcRock), and reports whether it did. A rock it built and installed at
// that version before is left as it is: a version of a release never changes.
func (s *srcRocks) install(
	ctx context.Context, dep manifest.LockDependency, servers []string,
) (bool, error) {
	if s == nil {
		return false, nil
	}

	needs, err := needsSrcRock(ctx, s.fetcher, dep, servers)
	if err != nil || !needs {
		return false, err
	}

	stamp := filepath.Join(s.dir, dep.Name+"-"+dep.Version+".built")
	if fileExists(stamp) && installed(ctx, s.tree, dep) {
		return true, nil
	}

	err = mkdir(s.dir)
	if err != nil {
		return false, err
	}

	dir, err := fetchSrcRock(ctx, s.fetcher, dep, s.dir, servers)
	if err != nil {
		return false, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	// A LuaRocks version is <version>-<revision>, with no dash in the version.
	revision := dep.Version[strings.LastIndex(dep.Version, "-")+1:]

	rev, err := strconv.Atoi(revision)
	if err != nil {
		return false, sdk.Errorf(sdk.ExitFailure, "%s %s: the version has no revision",
			dep.Name, dep.Version)
	}

	err = s.builder.buildManifest(ctx, manifestBuild{
		dep:       dep,
		dir:       dir,
		state:     filepath.Join(s.dir, dep.Name),
		revision:  rev,
		what:      "source rock",
		stale:     "the source rock is of another version than its name says",
		warnHooks: true,
		docs:      true,
	})
	if err != nil {
		return false, err
	}

	err = os.WriteFile(stamp, nil, srcRockFileMode)
	if err != nil {
		return false, fmt.Errorf("writing %s: %w", stamp, err)
	}

	return true, nil
}

// srcRockDocs copies the license files of the package of build, the entries
// of its license_files, into doc, each at its path in the package, as
// tt package pack puts them into doc/ of the binary rock of the release. An
// entry leading out of the package fails it. One the source rock does not
// hold is a warning, and the rock is installed without it: the source rock
// has the files git tracked where it was packed, while the binary rock has
// what the project had.
func (p pathManifests) srcRockDocs(build manifestBuild, entries []string, doc string) error {
	for _, entry := range entries {
		clean := filepath.Clean(entry)
		if clean == "." || !filepath.IsLocal(clean) {
			return sdk.Errorf(sdk.ExitFailure, "license_files entry %q: %w",
				entry, errSrcRockLicense)
		}

		src := filepath.Join(build.dir, clean)

		_, err := os.Lstat(src)
		if errors.Is(err, fs.ErrNotExist) {
			p.opts.emit([]string{fmt.Sprintf("the source rock of %s %s has no %s, a license "+
				"file of its manifest; the rock is installed without it",
				build.dep.Name, build.dep.Version, entry)})

			continue
		}

		if err != nil {
			return fmt.Errorf("license_files entry %q: %w", entry, err)
		}

		err = rocktree.MirrorTree(src, filepath.Join(doc, clean))
		if err != nil {
			return fmt.Errorf("license_files entry %q: %w", entry, err)
		}
	}

	return nil
}

// srcInstaller installs registry rocks as installer does, but those srcs
// builds from their source rocks (srcRocks.install).
type srcInstaller struct {
	installer rockInstaller
	srcs      *srcRocks
}

// Install installs the rock name at opts.Version, an exact version, as the
// lock pins it.
func (i srcInstaller) Install(ctx context.Context, name string, opts client.InstallOpts) error {
	dep := manifest.LockDependency{
		Name: name, Version: opts.Version, Source: sourceRegistry, Checksum: "",
		Path: "", ContentHash: "",
	}

	done, err := i.srcs.install(ctx, dep, opts.Servers)
	if err != nil || done {
		return err
	}

	return i.installer.Install(ctx, name, opts)
}

// newSrcRocks returns the srcRocks of a build of the project at projectDir
// with the adapter of the build, installing into its tree and building with
// builder.
func newSrcRocks(
	opts Options, adapter *rocks.Adapter, builder pathManifests,
) (*srcRocks, error) {
	cfg := adapter.Config()
	dir := filepath.Join(opts.ProjectDir, buildDirName, srcDirName)

	fetcher, err := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       cfg.Tree,
		WorkingDir: dir,
		Servers:    cfg.Servers,
		Logger:     opts.Logger,
	})).Client(client.BackendNative)
	if err != nil {
		return nil, fmt.Errorf("rocks client of the source rocks: %w", err)
	}

	return &srcRocks{fetcher: fetcher, dir: dir, tree: cfg.Tree, builder: builder}, nil
}

// installed reports whether the rocks tree holds dep at its version.
func installed(ctx context.Context, root string, dep manifest.LockDependency) bool {
	view, err := tree.Inspect(ctx, root)
	if err != nil {
		return false
	}

	return slices.Contains(view.Versions(dep.Name), dep.Version)
}

// needsSrcRock reports whether the locked registry rock dep is to be built
// from its source rock: servers, those it is installed from, have no rock of
// it this host installs - neither one of its arch nor an .all one - but a
// source rock. A server that has both is installed from as any other.
func needsSrcRock(
	ctx context.Context, fetcher srcRockFetcher, dep manifest.LockDependency, servers []string,
) (bool, error) {
	results, err := fetcher.Search(ctx, dep.Name, client.SearchOpts{
		Version: dep.Version, Source: false, Binary: false, All: false, Servers: servers,
	})
	if err != nil {
		return false, fmt.Errorf("looking for the rocks of %s %s: %w", dep.Name, dep.Version, err)
	}

	host := remote.HostArch()
	src := false

	for _, result := range results {
		if result.Name != dep.Name || result.Version != dep.Version ||
			!fromServers(result.Server, servers) {
			continue
		}

		switch result.Arch {
		case host, allArch:
			return false, nil
		case srcArch:
			src = true
		}
	}

	return src, nil
}

// fromServers reports whether server, as a search names the server a row came
// from, is one of servers: a search asks the configured servers too, after
// the ones it is given. An empty list is every server.
func fromServers(server string, servers []string) bool {
	if len(servers) == 0 {
		return true
	}

	normal := func(url string) string {
		return strings.TrimSuffix(strings.TrimPrefix(url, "file://"), "/")
	}

	for _, candidate := range servers {
		if normal(candidate) == normal(server) {
			return true
		}
	}

	return false
}

// fetchSrcRock downloads the source rock of the locked registry rock dep from
// servers into dir, unpacks it there and returns the directory of the tt
// package it holds: the one of its top-level directories with an
// app.manifest.toml, or dir itself when the manifest is at the root of the
// rock.
func fetchSrcRock(
	ctx context.Context, fetcher srcRockFetcher, dep manifest.LockDependency, dir string,
	servers []string,
) (string, error) {
	rock, err := fetcher.Download(ctx, dep.Name, client.DownloadOpts{
		Version: dep.Version, All: false, Source: true, Rockspec: false, Arch: "", Servers: servers,
	})
	if err != nil {
		return "", fmt.Errorf("downloading the source rock of %s %s: %w",
			dep.Name, dep.Version, err)
	}

	if !filepath.IsAbs(rock) {
		rock = filepath.Join(dir, rock)
	}

	unpacked := filepath.Join(dir, dep.Name+"-"+dep.Version)

	err = os.RemoveAll(unpacked)
	if err != nil {
		return "", fmt.Errorf("clearing %s: %w", unpacked, err)
	}

	err = unzipSrcRock(rock, unpacked)
	if err != nil {
		return "", fmt.Errorf("unpacking the source rock of %s %s: %w", dep.Name, dep.Version, err)
	}

	return srcRockPackage(unpacked, dep)
}

// srcRockPackage finds the directory of the tt package in the unpacked source
// rock dir.
func srcRockPackage(dir string, dep manifest.LockDependency) (string, error) {
	if fileExists(filepath.Join(dir, manifestFileName)) {
		return dir, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() && fileExists(filepath.Join(dir, entry.Name(), manifestFileName)) {
			return filepath.Join(dir, entry.Name()), nil
		}
	}

	return "", fmt.Errorf("%s %s: %w: tt builds the source rocks of tt packages only",
		dep.Name, dep.Version, errSrcRockPackage)
}

// fileExists reports whether path is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// unzipSrcRock unpacks the zip archive rock into dir. An entry that would
// land outside dir fails it; anything but a file or a directory is left out,
// and a file keeps its executable bit alone.
func unzipSrcRock(rock, dir string) error {
	archive, err := zip.OpenReader(rock)
	if err != nil {
		return fmt.Errorf("opening %s: %w", rock, err)
	}

	defer func() { _ = archive.Close() }()

	for _, file := range archive.File {
		err = unzipEntry(file, dir)
		if err != nil {
			return err
		}
	}

	return nil
}

// unzipEntry unpacks the entry file of a source rock into dir.
func unzipEntry(file *zip.File, dir string) error {
	name := filepath.FromSlash(file.Name)
	if filepath.IsAbs(name) || !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %s", errSrcRockEntry, file.Name)
	}

	dest := filepath.Join(dir, name)
	mode := file.Mode()

	switch {
	case mode.IsDir():
		return mkdir(dest)
	case !mode.IsRegular():
		return nil
	}

	err := mkdir(filepath.Dir(dest))
	if err != nil {
		return err
	}

	perm := srcRockFileMode
	if mode&0o111 != 0 {
		perm = srcRockExecMode
	}

	src, err := file.Open()
	if err != nil {
		return fmt.Errorf("reading %s: %w", file.Name, err)
	}

	defer func() { _ = src.Close() }()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}

	//nolint:gosec // A source rock is unpacked whole, as LuaRocks unpacks it.
	_, err = io.Copy(out, src)

	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}

	return nil
}

// mkdir creates dir and its parents.
func mkdir(dir string) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	return nil
}
