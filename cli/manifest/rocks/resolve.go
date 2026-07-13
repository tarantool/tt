package rocks

import (
	"context"
	"fmt"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/go-luarocks/remote"
)

// ResolvedRock is a rock chosen for a dependency: the name, the version picked
// from the winning server, and the URL of its resource on that server.
type ResolvedRock struct {
	// Name is the rock name.
	Name string
	// Version is the chosen version.
	Version luarocks.Version
	// URL is the .rock / .rockspec resource on the originating server.
	URL string
}

// orderedIndex queries a list of single-server indexes in order and returns
// the first server's results for a name (first-found-wins), instead of
// aggregating across all servers the way a multi-server HTTPRemoteIndex would.
// A server that errors (e.g. unreachable) is skipped; the last error is
// surfaced only when no server yields a rock.
type orderedIndex struct {
	indexes []luarocks.RemoteIndex
}

// newOrderedIndex builds an ordered index over the given per-server indexes.
func newOrderedIndex(indexes ...luarocks.RemoteIndex) *orderedIndex {
	return &orderedIndex{indexes: indexes}
}

// httpIndexes builds one HTTPRemoteIndex per server so they can be queried in
// order, each carrying the shared insecure-server list.
func httpIndexes(servers, insecure []string) []luarocks.RemoteIndex {
	out := make([]luarocks.RemoteIndex, 0, len(servers))

	for _, server := range servers {
		out = append(out, &remote.HTTPRemoteIndex{
			Servers:         []string{server},
			InsecureServers: insecure,
			UserAgent:       "",
			LuaVersion:      "",
			Arch:            "",
		})
	}

	return out
}

// Query asks each server in order and returns the first non-empty result,
// satisfying luarocks.RemoteIndex so the resolver can consume it too.
func (o *orderedIndex) Query(
	ctx context.Context, name, namespace string,
) ([]luarocks.VersionedRock, error) {
	var lastErr error

	for _, index := range o.indexes {
		found, err := index.Query(ctx, name, namespace)
		if err != nil {
			lastErr = err

			continue
		}

		if len(found) > 0 {
			return found, nil
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("query %q across servers: %w", name, lastErr)
	}

	return nil, nil
}

// Resolve finds the newest version of name that satisfies constraintExpr,
// querying servers in order (first-found-wins). A non-empty registry overrides
// the server list with that single server. An empty constraintExpr matches any
// version.
func (a *Adapter) Resolve(
	ctx context.Context, name, constraintExpr, registry string,
) (ResolvedRock, error) {
	index := a.index
	if registry != "" {
		index = newOrderedIndex(httpIndexes([]string{registry}, a.cfg.InsecureServers)...)
	}

	return resolveWith(ctx, index, name, constraintExpr)
}

// resolveWith is the index-agnostic core of Resolve, separated so tests can
// drive it with a fake index.
func resolveWith(
	ctx context.Context, index luarocks.RemoteIndex, name, constraintExpr string,
) (ResolvedRock, error) {
	candidates, err := index.Query(ctx, name, "")
	if err != nil {
		return ResolvedRock{}, fmt.Errorf("rocks: resolve %q: %w", name, err)
	}

	if len(candidates) == 0 {
		return ResolvedRock{}, fmt.Errorf("%q: %w", name, ErrNotFound)
	}

	constraints, err := deps.ParseConstraints(constraintExpr)
	if err != nil {
		return ResolvedRock{}, fmt.Errorf("rocks: parse constraints %q: %w", constraintExpr, err)
	}

	best, ok := pickNewest(candidates, constraints)
	if !ok {
		return ResolvedRock{}, fmt.Errorf("%q %q: %w", name, constraintExpr, ErrNoMatch)
	}

	return ResolvedRock{Name: best.Name, Version: best.Version, URL: best.URL}, nil
}

// pickNewest returns the highest-versioned candidate satisfying every
// constraint. An empty constraint list matches all. ok is false when nothing
// matches.
func pickNewest(
	candidates []luarocks.VersionedRock, constraints []luarocks.VersionConstraint,
) (luarocks.VersionedRock, bool) {
	var best luarocks.VersionedRock

	found := false

	for _, candidate := range candidates {
		if !deps.Match(candidate.Version, constraints) {
			continue
		}

		if !found || deps.Compare(candidate.Version, best.Version) > 0 {
			best = candidate
			found = true
		}
	}

	return best, found
}

// Checksum renders the source checksum for a resolved rock's rockspec as
// "md5:<hex>", taken from source.md5 (what LuaRocks publishes). ok is false
// when the rock publishes no md5; the caller decides the fallback (a
// content-hash of the materialized tree, or an empty checksum). The adapter
// only reports what the rock carried.
func Checksum(spec *luarocks.Rockspec) (string, bool) {
	if spec == nil || spec.Source.MD5 == "" {
		return "", false
	}

	return "md5:" + spec.Source.MD5, true
}
