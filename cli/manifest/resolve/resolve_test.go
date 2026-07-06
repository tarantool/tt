package resolve_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
	"github.com/tarantool/tt/cli/manifest/rocks"
	luarocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/deps"
)

// fakeAdapter is an in-memory registry standing in for cli/manifest/rocks so
// the resolution engine can be exercised without a live server. It mirrors the
// adapter's newest-that-fits selection and ErrNotFound / ErrNoMatch contract,
// routes by registry override, serves path-dependency rockspecs, and counts
// calls so tests can assert cross-product caching.
type fakeAdapter struct {
	servers   map[string][]*luarocks.Rockspec            // Default server candidates.
	scoped    map[string]map[string][]*luarocks.Rockspec // Registry -> name -> candidates.
	specs     map[string]*luarocks.Rockspec              // Artifact URL -> rockspec.
	local     map[string]*luarocks.Rockspec              // Path-dep dir -> rockspec.
	resolves  map[string]int                             // adapter.Resolve calls by name.
	metadatas map[string]int                             // adapter.Metadata calls by name.
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		servers:   map[string][]*luarocks.Rockspec{},
		scoped:    map[string]map[string][]*luarocks.Rockspec{},
		specs:     map[string]*luarocks.Rockspec{},
		local:     map[string]*luarocks.Rockspec{},
		resolves:  map[string]int{},
		metadatas: map[string]int{},
	}
}

// urlFor builds an artifact URL that, like a real registry, is server-specific:
// the same name and version on different registries yield different URLs.
func urlFor(registry, name, version string) string {
	if registry == "" {
		return name + "-" + version
	}

	return registry + "|" + name + "-" + version
}

func newSpec(name, version, md5 string, rockDeps []luarocks.Dep) *luarocks.Rockspec {
	spec := new(luarocks.Rockspec)

	spec.Package = name
	spec.Version = version
	spec.Source.MD5 = md5
	spec.Dependencies = rockDeps

	return spec
}

func (fake *fakeAdapter) Resolve(
	_ context.Context, name, constraintExpr, registry string,
) (rocks.ResolvedRock, error) {
	fake.resolves[name]++

	candidates := fake.servers[name]
	if registry != "" {
		candidates = fake.scoped[registry][name]
	}

	if len(candidates) == 0 {
		return rocks.ResolvedRock{}, rocks.ErrNotFound
	}

	constraints, parseErr := deps.ParseConstraints(constraintExpr)
	if parseErr != nil {
		return rocks.ResolvedRock{}, fmt.Errorf("fake: %w", parseErr)
	}

	var (
		best    *luarocks.Rockspec
		bestVer luarocks.Version
		found   bool
	)

	for _, candidate := range candidates {
		parsed, verErr := deps.ParseVersion(candidate.Version)
		if verErr != nil {
			continue
		}

		if !deps.Match(parsed, constraints) {
			continue
		}

		if !found || deps.Compare(parsed, bestVer) > 0 {
			best, bestVer, found = candidate, parsed, true
		}
	}

	if !found {
		return rocks.ResolvedRock{}, rocks.ErrNoMatch
	}

	url := urlFor(registry, name, best.Version)

	return rocks.ResolvedRock{Name: name, Version: bestVer, URL: url}, nil
}

func (fake *fakeAdapter) Metadata(
	_ context.Context, rock rocks.ResolvedRock,
) (*luarocks.Rockspec, error) {
	fake.metadatas[rock.Name]++

	spec, ok := fake.specs[rock.URL]
	if !ok {
		return nil, fmt.Errorf("fake: no metadata for %s", rock.URL)
	}

	return spec, nil
}

func (fake *fakeAdapter) LocalMetadata(dir string) (*luarocks.Rockspec, error) {
	spec, ok := fake.local[dir]
	if !ok {
		return nil, rocks.ErrNoLocalRockspec
	}

	return spec, nil
}

// add registers one rock version on the default servers with an md5 and zero or
// more dependencies.
func (fake *fakeAdapter) add(
	name, version, md5 string, rockDeps ...luarocks.Dep,
) *fakeAdapter {
	spec := newSpec(name, version, md5, rockDeps)

	fake.servers[name] = append(fake.servers[name], spec)
	fake.specs[urlFor("", name, version)] = spec

	return fake
}

// addScoped registers a rock version on the named registry only, so a
// dependency's registry override routes to it instead of the default servers.
func (fake *fakeAdapter) addScoped(
	registry, name, version, md5 string, rockDeps ...luarocks.Dep,
) *fakeAdapter {
	if fake.scoped[registry] == nil {
		fake.scoped[registry] = map[string][]*luarocks.Rockspec{}
	}

	spec := newSpec(name, version, md5, rockDeps)

	fake.scoped[registry][name] = append(fake.scoped[registry][name], spec)
	fake.specs[urlFor(registry, name, version)] = spec

	return fake
}

// addLocal registers the rockspec a path dependency's directory ships, so
// LocalMetadata returns its version and transitive dependencies.
func (fake *fakeAdapter) addLocal(
	dir, name, version string, rockDeps ...luarocks.Dep,
) *fakeAdapter {
	fake.local[dir] = newSpec(name, version, "", rockDeps)

	return fake
}

// dep builds a rockspec dependency from a name and a constraint expression.
func dep(t *testing.T, name, constraintExpr string) luarocks.Dep {
	t.Helper()

	constraints, err := deps.ParseConstraints(constraintExpr)
	require.NoError(t, err)

	return luarocks.Dep{Name: name, Constraints: constraints}
}

// parseManifest turns a manifest TOML body into a *Manifest.
func parseManifest(t *testing.T, body string) *manifest.Manifest {
	t.Helper()

	parsed, _, err := manifest.ParseManifest([]byte(body))
	require.NoError(t, err)

	return parsed
}

// depNames extracts the dependency names of a resolved product, in lock order.
func depNames(dependencies []manifest.LockDependency) []string {
	out := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		out = append(out, dependency.Name)
	}

	return out
}

// findDep returns the lock entry for name, or fails.
func findDep(
	t *testing.T, dependencies []manifest.LockDependency, name string,
) manifest.LockDependency {
	t.Helper()

	for _, dependency := range dependencies {
		if dependency.Name == name {
			return dependency
		}
	}

	t.Fatalf("dependency %q not in lock (%v)", name, depNames(dependencies))

	var zero manifest.LockDependency

	return zero
}

const oneProduct = `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[components.app]
path = '.'
[products.default]
components = ['app']
default = true
`

func TestDirectConstraintPicksHighestMatch(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.0.0-1", "aaa").
		add("metrics", "1.5.0-1", "bbb").
		add("metrics", "2.0.0-1", "ccc")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0,<2.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	got := findDep(t, lock.Products["default"].Dependencies, "metrics")
	assert.Equal(t, "1.5.0-1", got.Version)
	assert.Equal(t, "registry", got.Source)
	assert.Equal(t, "md5:bbb", got.Checksum)
}

func TestTransitiveClosure(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "m", dep(t, "checks", ">=3.0")).
		add("checks", "3.1.0-1", "c")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// Topological order: the transitive dep precedes the rock that needs it.
	dependencies := lock.Products["default"].Dependencies
	assert.Equal(t, []string{"checks", "metrics"}, depNames(dependencies))
}

func TestSharedTransitiveResolvesOnce(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("alpha", "1.0.0-1", "a", dep(t, "common", ">=1.0")).
		add("beta", "1.0.0-1", "b", dep(t, "common", ">=1.1")).
		add("common", "1.0.0-1", "c10").
		add("common", "1.2.0-1", "c12")

	man := parseManifest(t, oneProduct+`[dependencies]
alpha = '>=1.0.0'
beta = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	dependencies := lock.Products["default"].Dependencies

	commonCount := 0

	for _, dependency := range dependencies {
		if dependency.Name == "common" {
			commonCount++
		}
	}

	assert.Equal(t, 1, commonCount,
		"shared transitive must appear once: %v", depNames(dependencies))
	// The first branch (alpha) pins common to the newest satisfying >=1.0; the
	// second branch's >=1.1 is compatible with that pick.
	assert.Equal(t, "1.2.0-1", findDep(t, dependencies, "common").Version)
}

func TestIncompatibleConstraintsConflict(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("alpha", "1.0.0-1", "a", dep(t, "common", ">=2.0")).
		add("beta", "1.0.0-1", "b", dep(t, "common", "<2.0")).
		add("common", "1.0.0-1", "c10").
		add("common", "2.0.0-1", "c20")

	man := parseManifest(t, oneProduct+`[dependencies]
alpha = '>=1.0.0'
beta = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	_, _, err := engine.Resolve(context.Background(), man)
	require.ErrorIs(t, err, resolve.ErrConflict)
	assert.Contains(t, err.Error(), "common")
}

func TestMissingMD5Warns(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().add("metrics", "1.0.0-1", "")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "no md5")
	assert.Empty(t, findDep(t, lock.Products["default"].Dependencies, "metrics").Checksum)
}
