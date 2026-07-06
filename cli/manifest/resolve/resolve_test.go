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
// adapter's newest-that-fits selection and ErrNotFound / ErrNoMatch contract.
type fakeAdapter struct {
	registry map[string][]*luarocks.Rockspec
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{registry: map[string][]*luarocks.Rockspec{}}
}

func (fake *fakeAdapter) Resolve(
	_ context.Context, name, constraintExpr, _ string,
) (rocks.ResolvedRock, error) {
	candidates := fake.registry[name]
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

	return rocks.ResolvedRock{Name: name, Version: bestVer, URL: name + "-" + best.Version}, nil
}

func (fake *fakeAdapter) Metadata(
	_ context.Context, rock rocks.ResolvedRock,
) (*luarocks.Rockspec, error) {
	for _, candidate := range fake.registry[rock.Name] {
		if candidate.Version == rock.Version.Raw {
			return candidate, nil
		}
	}

	return nil, fmt.Errorf("fake: no metadata for %s %s", rock.Name, rock.Version.Raw)
}

func (fake *fakeAdapter) LocalMetadata(_ string) (*luarocks.Rockspec, error) {
	return nil, rocks.ErrNoLocalRockspec
}

// add registers one rock version with an md5 and zero or more dependencies.
// Fields are assigned rather than composed so the fixture stays exhaustive
// without listing every rockspec field.
func (fake *fakeAdapter) add(
	name, version, md5 string, rockDeps ...luarocks.Dep,
) *fakeAdapter {
	spec := new(luarocks.Rockspec)

	spec.Package = name
	spec.Version = version
	spec.Source.MD5 = md5
	spec.Dependencies = rockDeps

	fake.registry[name] = append(fake.registry[name], spec)

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
