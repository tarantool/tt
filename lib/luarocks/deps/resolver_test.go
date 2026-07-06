package deps_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/deps"
)

// bareIndex is a RemoteIndex that returns name/version/URL rows without a
// preloaded Spec, like remote.HTTPRemoteIndex.
type bareIndex map[string][]rocks.VersionedRock

func (b bareIndex) Query(_ context.Context, name string) ([]rocks.VersionedRock, error) {
	return b[name], nil
}

func mustVer(t *testing.T, raw string) rocks.Version {
	t.Helper()

	v, err := deps.ParseVersion(raw)
	require.NoError(t, err)

	return v
}

func mustDep(t *testing.T, name, expr string) rocks.Dep {
	t.Helper()

	cs, err := deps.ParseConstraints(expr)
	require.NoError(t, err)

	return rocks.Dep{Name: name, Constraints: cs}
}

// TestResolveWithoutFetcherStopsAtPreloadedSpecs shows the baseline: a bare
// index that preloads nothing resolves only the root's direct dependencies.
func TestResolveWithoutFetcherStopsAtPreloadedSpecs(t *testing.T) {
	idx := bareIndex{
		"a": {{Name: "a", Version: mustVer(t, "1.0.0-1"), URL: "a-1.0.0-1"}},
		"b": {{Name: "b", Version: mustVer(t, "2.0.0-1"), URL: "b-2.0.0-1"}},
	}

	root := &rocks.Rockspec{Package: "root", Dependencies: []rocks.Dep{mustDep(t, "a", ">=1.0")}}

	steps, err := deps.Resolve(context.Background(), root, idx)
	require.NoError(t, err)

	names := make([]string, 0, len(steps))
	for _, s := range steps {
		names = append(names, s.Name)
	}

	// Without a fetcher, a's transitive dep on b is never discovered.
	assert.Equal(t, []string{"a"}, names)
}

// TestResolveWithSpecFetcherWalksTransitively shows the fetcher completing the
// closure, fetching only the chosen version of each name.
func TestResolveWithSpecFetcherWalksTransitively(t *testing.T) {
	idx := bareIndex{
		"a": {
			{Name: "a", Version: mustVer(t, "0.5.0-1"), URL: "a-0.5.0-1"},
			{Name: "a", Version: mustVer(t, "1.0.0-1"), URL: "a-1.0.0-1"},
		},
		"b": {{Name: "b", Version: mustVer(t, "2.0.0-1"), URL: "b-2.0.0-1"}},
	}

	specs := map[string]*rocks.Rockspec{
		"a-1.0.0-1": {Package: "a", Dependencies: []rocks.Dep{mustDep(t, "b", ">=1.0")}},
		"b-2.0.0-1": {Package: "b"},
	}

	var fetched []string

	fetcher := func(_ context.Context, rock rocks.VersionedRock) (*rocks.Rockspec, error) {
		fetched = append(fetched, rock.URL)

		spec, ok := specs[rock.URL]
		if !ok {
			return nil, fmt.Errorf("no spec for %s", rock.URL)
		}

		return spec, nil
	}

	root := &rocks.Rockspec{Package: "root", Dependencies: []rocks.Dep{mustDep(t, "a", ">=1.0")}}

	steps, err := deps.Resolve(context.Background(), root, idx, deps.WithSpecFetcher(fetcher))
	require.NoError(t, err)

	names := make([]string, 0, len(steps))
	for _, s := range steps {
		names = append(names, s.Name)
	}

	// Full closure, deepest-first topo order.
	assert.Equal(t, []string{"b", "a"}, names)
	// Only the chosen version of each name is fetched: a-1.0.0-1 (not the
	// rejected a-0.5.0-1) and b-2.0.0-1.
	assert.ElementsMatch(t, []string{"a-1.0.0-1", "b-2.0.0-1"}, fetched)
}

// TestResolveSpecFetcherErrorAborts shows that a spec fetcher failure aborts the
// resolution with the wrapped error, rather than silently truncating the
// transitive closure of the rock whose rockspec could not be fetched.
func TestResolveSpecFetcherErrorAborts(t *testing.T) {
	idx := bareIndex{
		"a": {{Name: "a", Version: mustVer(t, "1.0.0-1"), URL: "a-1.0.0-1"}},
	}

	errFetch := errors.New("network down")

	fetcher := func(_ context.Context, _ rocks.VersionedRock) (*rocks.Rockspec, error) {
		return nil, errFetch
	}

	root := &rocks.Rockspec{Package: "root", Dependencies: []rocks.Dep{mustDep(t, "a", ">=1.0")}}

	_, err := deps.Resolve(context.Background(), root, idx, deps.WithSpecFetcher(fetcher))
	require.ErrorIs(t, err, errFetch)
	assert.Contains(t, err.Error(), "a")
}
