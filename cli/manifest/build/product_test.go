package build

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

func multiProductManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Package: manifest.Package{Name: "my-app"},
		Components: map[string]manifest.Component{
			"lua":    {Path: "."},
			"native": {Path: "native/"},
			"extra":  {Path: "extra/"},
		},
		Products: map[string]manifest.Product{
			"default": {Components: []string{"lua", "native"}, Default: true},
			"minimal": {Components: []string{"lua"}},
		},
	}
}

func TestSelectProduct_default(t *testing.T) {
	t.Parallel()

	name, product, err := selectProduct(multiProductManifest(), "")
	require.NoError(t, err)
	assert.Equal(t, "default", name)
	assert.Equal(t, []string{"lua", "native"}, product.Components)
}

func TestSelectProduct_named(t *testing.T) {
	t.Parallel()

	name, product, err := selectProduct(multiProductManifest(), "minimal")
	require.NoError(t, err)
	assert.Equal(t, "minimal", name)
	assert.Equal(t, []string{"lua"}, product.Components)
}

func TestSelectProduct_unknown(t *testing.T) {
	t.Parallel()

	_, _, err := selectProduct(multiProductManifest(), "nope")
	assert.True(t, errors.Is(err, errUnknownProduct))
}

func TestSelectProduct_singleImplicitDefault(t *testing.T) {
	t.Parallel()

	man := &manifest.Manifest{
		Components: map[string]manifest.Component{"lua": {Path: "."}},
		Products: map[string]manifest.Product{
			"solo": {Components: []string{"lua"}},
		},
	}
	name, _, err := selectProduct(man, "")
	require.NoError(t, err)
	assert.Equal(t, "solo", name)
}

// TestSelectProduct_noProducts covers a manifest declaring no [products]: the
// build acts on the implicit product, built from every component, instead of
// refusing.
func TestSelectProduct_noProducts(t *testing.T) {
	t.Parallel()

	man := &manifest.Manifest{
		Components: map[string]manifest.Component{
			"native": {Path: "native"},
			"lua":    {Path: "."},
		},
	}

	name, product, err := selectProduct(man, "")
	require.NoError(t, err)
	assert.Equal(t, manifest.ImplicitProduct, name)
	assert.Equal(t, []string{"lua", "native"}, product.Components)
	assert.True(t, product.Default)

	// The implicit product is selectable by name like a declared one.
	name, _, err = selectProduct(man, manifest.ImplicitProduct)
	require.NoError(t, err)
	assert.Equal(t, manifest.ImplicitProduct, name)
}

func TestSelectComponents_all(t *testing.T) {
	t.Parallel()

	product := manifest.Product{Components: []string{"lua", "native"}}
	got, err := selectComponents(product, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"lua", "native"}, got)
}

func TestSelectComponents_one(t *testing.T) {
	t.Parallel()

	product := manifest.Product{Components: []string{"lua", "native"}}
	got, err := selectComponents(product, "native")
	require.NoError(t, err)
	assert.Equal(t, []string{"native"}, got)
}

func TestSelectComponents_unknown(t *testing.T) {
	t.Parallel()

	product := manifest.Product{Components: []string{"lua"}}
	_, err := selectComponents(product, "native")
	assert.True(t, errors.Is(err, errUnknownComponent))
}
