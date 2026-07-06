package resolve

import (
	"context"
	"errors"

	"github.com/tarantool/tt/cli/manifest/rocks"
	luarocks "github.com/tarantool/tt/lib/luarocks"
)

// resolveCache memoizes the adapter calls and directory hashing across a single
// Engine.Resolve run, so products that share a dependency resolve, fetch and
// hash it once rather than once per product. It is scoped to one resolution
// pass; a rock version's rockspec is immutable, and a requirement is keyed by
// its exact (name, constraint, registry), so every cached value is sound to
// reuse within the run. Not safe for concurrent use - resolution is sequential.
type resolveCache struct {
	resolved map[string]rocks.ResolvedRock
	metadata map[string]*luarocks.Rockspec
	content  map[string]string
	// local caches LocalMetadata by directory. A cached leaf (a directory with
	// no rockspec) stores a nil rockspec; the comma-ok on the read distinguishes
	// it (present, nil) from a not-yet-cached directory (absent).
	local map[string]*luarocks.Rockspec
	// warnedNoMD5 records the artifact URLs whose rock published no md5 so the
	// reproducibility warning is emitted once per run, not once per product that
	// shares the rock (the walker is per-product, but this cache is run-wide).
	warnedNoMD5 map[string]bool
}

func newResolveCache() *resolveCache {
	return &resolveCache{
		resolved:    map[string]rocks.ResolvedRock{},
		metadata:    map[string]*luarocks.Rockspec{},
		content:     map[string]string{},
		local:       map[string]*luarocks.Rockspec{},
		warnedNoMD5: map[string]bool{},
	}
}

// markNoMD5 records that the rock at url published no md5 and reports whether
// this is the first time in the run - so a checksum-less rock shared by several
// products warns once, not once per product.
func (c *resolveCache) markNoMD5(url string) bool {
	if c.warnedNoMD5[url] {
		return false
	}

	c.warnedNoMD5[url] = true

	return true
}

// resolveRock memoizes adapter.Resolve by (name, constraint, registry). Errors
// are not cached - a transient failure should be retryable on the next product.
func (c *resolveCache) resolveRock(
	ctx context.Context, adapter Adapter, name, constraintExpr, registry string,
) (rocks.ResolvedRock, error) {
	key := name + "\x00" + constraintExpr + "\x00" + registry

	hit, ok := c.resolved[key]
	if ok {
		return hit, nil
	}

	got, err := adapter.Resolve(ctx, name, constraintExpr, registry)
	if err != nil {
		return rocks.ResolvedRock{}, err //nolint:wrapcheck // resolveOne adds context
	}

	c.resolved[key] = got

	return got, nil
}

// rockMetadata memoizes adapter.Metadata by the resolved artifact URL. The URL,
// not (name, version), is the identity of the fetched rockspec: the same name
// and version can come from different servers (a per-dependency registry
// override, or the default multi-server order) with different source.md5 and
// different transitive dependencies, so keying by version alone would share the
// wrong rockspec across products.
func (c *resolveCache) rockMetadata(
	ctx context.Context, adapter Adapter, rock rocks.ResolvedRock,
) (*luarocks.Rockspec, error) {
	key := rock.URL

	hit, ok := c.metadata[key]
	if ok {
		return hit, nil
	}

	got, err := adapter.Metadata(ctx, rock)
	if err != nil {
		return nil, err //nolint:wrapcheck // resolveOne adds the dependency context
	}

	c.metadata[key] = got

	return got, nil
}

// dirContentHash memoizes contentHash by directory.
func (c *resolveCache) dirContentHash(dir string) (string, error) {
	hit, ok := c.content[dir]
	if ok {
		return hit, nil
	}

	got, err := contentHash(dir)
	if err != nil {
		return "", err
	}

	c.content[dir] = got

	return got, nil
}

// localMetadata memoizes adapter.LocalMetadata by directory. A directory with
// no rockspec (rocks.ErrNoLocalRockspec) is cached as a leaf: a nil rockspec
// and no error.
func (c *resolveCache) localMetadata(adapter Adapter, dir string) (*luarocks.Rockspec, error) {
	hit, ok := c.local[dir]
	if ok {
		return hit, nil
	}

	spec, err := adapter.LocalMetadata(dir)
	if err != nil && !errors.Is(err, rocks.ErrNoLocalRockspec) {
		return nil, err //nolint:wrapcheck // resolvePath adds the dependency context
	}

	c.local[dir] = spec

	return spec, nil
}
