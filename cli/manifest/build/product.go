package build

import (
	"fmt"
	"slices"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// selectProduct picks the product the build acts on, among the manifest's
// effective products - so a manifest declaring none builds its implicit one. A
// non-empty name selects that product (errUnknownProduct if it does not exist).
// With no name: a single product is used implicitly; otherwise the one flagged
// default is chosen (errNoDefaultProduct if none is, which Validate normally
// rejects first). It returns the product name so callers can index the lock's
// per-product closure.
func selectProduct(man *manifest.Manifest, name string) (string, manifest.Product, error) {
	products := man.EffectiveProducts()

	if name != "" {
		product, ok := products[name]
		if !ok {
			return "", manifest.Product{}, fmt.Errorf("%w: %q", errUnknownProduct, name)
		}

		return name, product, nil
	}

	chosen, product, ok := man.DefaultProduct()
	if !ok {
		return "", manifest.Product{}, errNoDefaultProduct
	}

	return chosen, product, nil
}

// selectComponents returns the component names the build processes for product:
// every component of the product, or those named when names are given, in
// the order of the product (errUnknownComponent if a name is not in the
// product). An empty name names none.
func selectComponents(product manifest.Product, names ...string) ([]string, error) {
	names = slices.DeleteFunc(slices.Clone(names), func(name string) bool { return name == "" })
	if len(names) == 0 {
		return product.Components, nil
	}

	for _, name := range names {
		if !slices.Contains(product.Components, name) {
			return nil, fmt.Errorf("%w: %q is not a component of the selected product",
				errUnknownComponent, name)
		}
	}

	return slices.DeleteFunc(slices.Clone(product.Components), func(name string) bool {
		return !slices.Contains(names, name)
	}), nil
}
