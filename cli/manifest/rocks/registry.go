package rocks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	luarocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/client"
	"github.com/tarantool/tt/lib/luarocks/fetch"
	"github.com/tarantool/tt/lib/luarocks/rockspec"
)

// Metadata fetches a resolved rock's rockspec and evaluates it into a typed
// Rockspec, with the runtime platforms merged in. The result carries
// source.md5, which Checksum turns into the lock checksum. A temporary working
// directory is used and removed; the spec is fully materialized before return.
func (a *Adapter) Metadata(ctx context.Context, rock ResolvedRock) (*luarocks.Rockspec, error) {
	dir, err := os.MkdirTemp("", "tt-rocks-meta-")
	if err != nil {
		return nil, fmt.Errorf("rocks: temp dir: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	unpacked, err := fetch.FetchWith(ctx, rock.URL, dir, fetch.Options{
		InsecureServers: a.cfg.InsecureServers,
		UserAgent:       "",
		Tag:             "",
		Branch:          "",
	})
	if err != nil {
		return nil, fmt.Errorf("rocks: fetch %s: %w", rock.URL, err)
	}

	specPath, err := findRockspec(unpacked)
	if err != nil {
		return nil, err
	}

	spec, err := rockspec.Eval(specPath, a.cfg.Rockspec)
	if err != nil {
		return nil, fmt.Errorf("rocks: eval %s: %w", specPath, err)
	}

	rockspec.MergePlatforms(spec, rockspec.RuntimePlatforms())

	return spec, nil
}

// FetchSource downloads and unpacks a rock's upstream source (the rockspec
// source.url) into destDir, honoring source.tag / source.branch and the
// adapter's insecure-server list. It returns the unpacked working-tree path.
// Generic rocks from luarocks.org go through the same path as Tarantool rocks.
func (a *Adapter) FetchSource(
	ctx context.Context, spec *luarocks.Rockspec, destDir string,
) (string, error) {
	unpacked, err := fetch.FetchWith(ctx, spec.Source.URL, destDir, fetch.Options{
		InsecureServers: a.cfg.InsecureServers,
		UserAgent:       "",
		Tag:             spec.Source.Tag,
		Branch:          spec.Source.Branch,
	})
	if err != nil {
		return "", fmt.Errorf("rocks: fetch source %s: %w", spec.Source.URL, err)
	}

	return unpacked, nil
}

// Search queries the configured registries for rocks matching pattern. It runs
// on the lua backend (the native backend does not implement search); the caller
// need not know which backend is used.
func (a *Adapter) Search(
	ctx context.Context, pattern string, opts client.SearchOpts,
) ([]client.SearchResult, error) {
	rocksClient, err := a.Client(client.BackendLua)
	if err != nil {
		return nil, err
	}

	results, err := rocksClient.Search(ctx, pattern, opts)
	if err != nil {
		return nil, fmt.Errorf("rocks: search %q: %w", pattern, err)
	}

	return results, nil
}

// Download fetches a rock file for name into the working directory and returns
// its path. Like Search it runs on the lua backend behind the wrapper.
func (a *Adapter) Download(
	ctx context.Context, name string, opts client.DownloadOpts,
) (string, error) {
	rocksClient, err := a.Client(client.BackendLua)
	if err != nil {
		return "", err
	}

	path, err := rocksClient.Download(ctx, name, opts)
	if err != nil {
		return "", fmt.Errorf("rocks: download %q: %w", name, err)
	}

	return path, nil
}

// findRockspec returns the single top-level *.rockspec in dir. It does not
// recurse: a bare .rockspec download is one top-level file, and a .src.rock
// carries its rockspec at the archive root, while the bundled source tree may
// ship unrelated rockspecs deeper down. More than one top-level rockspec is
// ambiguous - which version to pin is undecidable - so it is an error rather
// than a silent alphabetical pick, matching client.findRockspecIn and upstream
// luarocks make.
func findRockspec(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("rocks: read %s: %w", dir, err)
	}

	found := ""

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".rockspec") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		if found != "" {
			return "", fmt.Errorf("rocks: %w in %s: %s and %s",
				errMultipleRockspec, dir, filepath.Base(found), entry.Name())
		}

		found = path
	}

	if found == "" {
		return "", fmt.Errorf("rocks: %w: %s", errNoRockspec, dir)
	}

	return found, nil
}
