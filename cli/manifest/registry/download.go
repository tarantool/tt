package registry

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

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/rocks"
)

const (
	// lockFileName is the lock the no-argument form mirrors.
	lockFileName = "app.manifest.lock"
	// sourceRegistry is the lock's source value for a dependency that comes
	// from a rock server, the only kind a mirror can hold.
	sourceRegistry = "registry"
	// makeManifest is the LuaRocks admin command that indexes a directory of
	// rock files into the `manifest` that makes it a server.
	makeManifest = "make_manifest"
	// dirPerm is the mode a created download directory gets.
	dirPerm fs.FileMode = 0o755
)

// Ref is one rock to download: a name, and optionally the exact version.
type Ref struct {
	// Name is the rock name, possibly namespaced.
	Name string
	// Version is the exact version, empty for "whatever the servers offer".
	Version string
}

// ParseRef reads a NAME or NAME@VERSION argument.
//
// The separator is "@" rather than a space or a second positional because a
// download takes a list, and a list of pairs cannot be written positionally
// without the shell's word order deciding which name a version belongs to.
func ParseRef(arg string) (Ref, error) {
	name, version, hasVersion := strings.Cut(arg, "@")

	switch {
	case strings.TrimSpace(name) == "":
		return Ref{}, stateErrorf("%w %q: no rock name", ErrBadReference, arg)
	case hasVersion && strings.TrimSpace(version) == "":
		return Ref{}, stateErrorf("%w %q: no version after %q", ErrBadReference, arg, "@")
	}

	return Ref{Name: name, Version: version}, nil
}

// DownloadOptions configures one download run.
type DownloadOptions struct {
	// Refs are the rocks to download. Empty mirrors the project's lock.
	Refs []Ref
	// Dir is the directory the rock files land in, created if missing.
	// Required and must be absolute.
	Dir string
	// ProjectDir is where the lock is read from when Refs is empty.
	ProjectDir string
	// Registries is the effective server list, queried in order.
	Registries []rocks.Registry
	// Tarantool carries the Tarantool facts the engine configuration needs.
	Tarantool rocks.TarantoolInfo
	// Warn receives non-fatal diagnostics, such as a locked dependency that
	// has no place in a mirror. Nil drops them.
	Warn func(string)
}

// emit surfaces a non-fatal diagnostic through the Warn sink, if any.
func (o DownloadOptions) emit(msg string) {
	if o.Warn != nil {
		o.Warn(msg)
	}
}

// Download fetches rock files into opts.Dir and indexes the directory, so what
// it leaves behind is a rock server rather than a pile of files.
//
// With no refs it mirrors the project's locked closure - every product's
// dependencies plus the dev closure - at the exact locked versions. A
// dependency that does not come from a registry has no artifact to mirror and
// is reported and skipped.
//
// The run is idempotent: a rock already in the directory is overwritten and
// the index is rebuilt from whatever the directory then holds, so re-running
// after adding a dependency extends the mirror rather than requiring a fresh
// one.
func Download(ctx context.Context, opts DownloadOptions) ([]string, error) {
	refs, err := downloadRefs(opts)
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(opts.Dir, dirPerm)
	if err != nil {
		return nil, stateErrorf("creating %s: %w", opts.Dir, err)
	}

	// The adapter's working directory is where the engine writes a downloaded
	// file, which is what puts the rocks in opts.Dir.
	adapter := adapterFor(opts.Tarantool, opts.Registries, opts.Dir)

	written := make([]string, 0, len(refs))

	for _, ref := range refs {
		path, downloadErr := adapter.Download(ctx, ref.Name, client.DownloadOpts{
			Version:  ref.Version,
			All:      false,
			Source:   false,
			Rockspec: false,
			Arch:     "",
			// The effective list is already the adapter's configured list;
			// naming it here again would query every server twice.
			Servers: nil,
		})
		if downloadErr != nil {
			return nil, stateErrorf("downloading %s: %w", ref, downloadErr)
		}

		written = append(written, path)
	}

	err = index(ctx, adapter, opts.Dir)
	if err != nil {
		return nil, err
	}

	slices.Sort(written)

	return slices.Compact(written), nil
}

// String renders a ref the way it is written on the command line.
func (r Ref) String() string {
	if r.Version == "" {
		return r.Name
	}

	return r.Name + "@" + r.Version
}

// index writes the LuaRocks `manifest` that turns the directory into a rock
// server.
//
// It runs on the Lua backend because the native engine implements no admin
// command. That is the one place this pipeline still needs the interpreter,
// and it is a local operation over a directory: nothing is fetched, so it
// costs a VM start and no network.
func index(ctx context.Context, adapter *rocks.Adapter, dir string) error {
	rocksClient, err := adapter.Client(client.BackendLua)
	if err != nil {
		return stateErrorf("%w", err)
	}

	err = rocksClient.Admin(ctx, makeManifest, []string{dir}, client.AdminOpts{
		Server: "",
		Force:  false,
	})
	if err != nil {
		return stateErrorf("indexing %s: %w", dir, err)
	}

	return nil
}

// downloadRefs returns the refs to download: the ones given on the command
// line, or the project's locked closure when none were.
func downloadRefs(opts DownloadOptions) ([]Ref, error) {
	if len(opts.Refs) > 0 {
		return opts.Refs, nil
	}

	return lockedRefs(opts)
}

// lockedRefs reads the project lock and returns every registry dependency it
// pins, deduplicated and in name order.
//
// Products share most of their closure, and the dev closure overlaps both, so
// the same rock is normally pinned several times at the same version;
// downloading it once per mention would fetch the same file repeatedly.
func lockedRefs(opts DownloadOptions) ([]Ref, error) {
	lock, err := readLock(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	seen := map[Ref]bool{}
	refs := []Ref{}

	collect := func(deps []manifest.LockDependency) {
		for _, dep := range deps {
			if dep.Source != sourceRegistry {
				opts.emit(fmt.Sprintf(
					"%s is a %s dependency and has no rock file to mirror; skipped",
					dep.Name, dep.Source,
				))

				continue
			}

			ref := Ref{Name: dep.Name, Version: dep.Version}
			if seen[ref] {
				continue
			}

			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	for _, product := range slices.Sorted(maps.Keys(lock.Products)) {
		collect(lock.Products[product].Dependencies)
	}

	collect(lock.DevDependencies)

	slices.SortFunc(refs, func(a, b Ref) int {
		return strings.Compare(a.String(), b.String())
	})

	return refs, nil
}

// readLock loads the project's lock, reporting the command that writes one
// when there is none: a mirror of a closure needs the closure to exist.
func readLock(projectDir string) (*manifest.Lock, error) {
	if projectDir == "" {
		return nil, stateErrorf("%w: name the rocks to download, or run in a project",
			ErrNoLock)
	}

	path := filepath.Join(projectDir, lockFileName)

	data, err := os.ReadFile(path) //nolint:gosec // Reads the caller's own lock.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, stateErrorf("%w: run tt package resolve first", ErrNoLock)
		}

		return nil, stateErrorf("reading %s: %w", lockFileName, err)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil, stateErrorf("parsing %s: %w", lockFileName, err)
	}

	return lock, nil
}
