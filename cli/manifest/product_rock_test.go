package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// productRocks are the components of the products below: a module, its tools
// and a role.
const productRocks = `
[components.lib]
path = "lib"
[components.tools]
path = "tools"
[components.role]
path = "role"
`

// TestProductRock: the default product is released as the rock of the
// package, another one as the rock it names, or as none.
func TestProductRock(t *testing.T) {
	t.Parallel()

	mfst := parseBody(t, productRocks+`
[products.default]
components = ["lib"]
default = true
[products.tools]
components = ["tools"]
rock = "demo-tools"
[products.full]
components = ["lib", "tools", "role"]
`)

	_, err := mfst.Validate()
	require.NoError(t, err)

	assert.Equal(t, "demo", mfst.ProductRock("default"))
	assert.Equal(t, "demo-tools", mfst.ProductRock("tools"))
	assert.Empty(t, mfst.ProductRock("full"))
	assert.Empty(t, mfst.ProductRock("missing"))

	product, ok := mfst.RockProduct("demo-tools")
	require.True(t, ok)
	assert.Equal(t, "tools", product)

	product, ok = mfst.RockProduct("demo")
	require.True(t, ok)
	assert.Equal(t, "default", product)

	_, ok = mfst.RockProduct("full")
	assert.False(t, ok)

	// A manifest without products is released as the rock of the package.
	bare := parseBody(t, productRocks)
	assert.Equal(t, "demo", bare.ProductRock("default"))
}

// TestProductRockValidation: a rock name is a name, one rock is one product,
// and no component is in two rocks.
func TestProductRockValidation(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		`
[products.default]
components = ["lib"]
default = true
[products.tools]
components = ["tools"]
rock = "Demo_Tools"
`: "products.tools.rock: name must match",
		`
[products.default]
components = ["lib"]
default = true
[products.tools]
components = ["tools"]
rock = "demo"
`: `the product default is released as the rock "demo" too`,
		`
[products.default]
components = ["lib", "tools"]
default = true
[products.tools]
components = ["tools"]
rock = "demo-tools"
`: `the component "tools" is in the rock "demo" too`,
	} {
		_, err := parseBody(t, productRocks+body).Validate()
		require.Error(t, err, body)
		assert.Contains(t, err.Error(), want, body)
	}
}
