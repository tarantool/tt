package build

import (
	"fmt"
	"slices"

	"github.com/tarantool/tt/cli/manifest"
)

// selectProduct picks the product the build acts on. A non-empty name selects
// that product (errUnknownProduct if it does not exist). With no name: a single
// product is used implicitly; otherwise the one flagged default is chosen
// (errNoDefaultProduct if none is, which Validate normally rejects first). It
// returns the product name so callers can index the lock's per-product closure.
func selectProduct(man *manifest.Manifest, name string) (string, manifest.Product, error) {
	if len(man.Products) == 0 {
		return "", manifest.Product{}, errNoProducts
	}

	if name != "" {
		product, ok := man.Products[name]
		if !ok {
			return "", manifest.Product{}, fmt.Errorf("%w: %q", errUnknownProduct, name)
		}

		return name, product, nil
	}

	if len(man.Products) == 1 {
		for only, product := range man.Products {
			return only, product, nil
		}
	}

	for chosen, product := range man.Products {
		if product.Default {
			return chosen, product, nil
		}
	}

	return "", manifest.Product{}, errNoDefaultProduct
}

// selectComponents returns the component names the build processes for product:
// every component of the product, or the single named one when component is
// non-empty (errUnknownComponent if that name is not in the product).
func selectComponents(product manifest.Product, component string) ([]string, error) {
	if component == "" {
		return product.Components, nil
	}

	if slices.Contains(product.Components, component) {
		return []string{component}, nil
	}

	return nil, fmt.Errorf("%w: %q is not a component of the selected product",
		errUnknownComponent, component)
}
