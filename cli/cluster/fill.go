package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	goconfig "github.com/tarantool/go-config/v2"
)

// fillOnlyMerge fills dst with what src has and dst has not, the way
// Tarantool merges a config source under one of a higher priority, such as
// the config storage under the config file: maps merge key by key, and
// anything else, an array included, is taken whole or not at all. dst keeps
// every value it has, and src fills in a key dst lacks or holds null at.
//
// An array is never merged by its items: listen [a] of dst stays [a] over
// listen [b, c] of src, and an array that only src has comes over as an
// array. Setting its items one by one would make dst hold a map of "0",
// "1" and so on instead. An empty map of src adds nothing to a map of dst.
// The keys src fills in keep the order they have in src.
func fillOnlyMerge(ctx context.Context, dst *goconfig.MutableConfig, src goconfig.Config) error {
	add, found, err := rawValue(src, nil)
	if err != nil || !found {
		return err
	}

	order, err := keyOrder(ctx, src)
	if err != nil {
		return err
	}

	have, _, err := rawValue(dst.Snapshot(), nil)
	if err != nil {
		return err
	}

	return fillMissing(dst, nil, have, add, order)
}

// fillMissing fills dst at path, where dst holds have, with add of src: the
// whole of add where dst holds nothing, or an empty map and add is a map, and
// the keys of add that dst lacks where both are maps. Any other value dst
// holds stays.
func fillMissing(dst *goconfig.MutableConfig, path goconfig.KeyPath, have, add any,
	order map[string][]string,
) error {
	if add == nil {
		return nil
	}

	haveMap, haveIsMap := have.(map[string]any)
	addMap, addIsMap := add.(map[string]any)

	switch {
	case have == nil && addIsMap && len(addMap) > 0:
		// Key by key, so that the keys keep their order: a map set whole
		// has its keys sorted.
	case have == nil, haveIsMap && len(haveMap) == 0 && addIsMap && len(addMap) > 0:
		err := dst.Set(path, add)
		if err != nil {
			return fmt.Errorf("fill %s: %w", path, err)
		}

		return nil
	case !haveIsMap || !addIsMap:
		return nil
	}

	for _, key := range orderedKeys(order, path, addMap) {
		err := fillMissing(dst, path.Append(key), haveMap[key], addMap[key], order)
		if err != nil {
			return err
		}
	}

	return nil
}

// rawValue reads the value of cfg at path as maps, arrays and scalars, and
// reports whether there is one.
func rawValue(cfg goconfig.Config, path goconfig.KeyPath) (any, bool, error) {
	if _, ok := cfg.Lookup(path); !ok {
		return nil, false, nil
	}

	var value any

	_, err := cfg.Get(path, &value)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}

	return value, true, nil
}

// keyOrder returns the keys of every map of cfg in the order cfg holds them,
// by the path of the map. A map that cfg reads into is a Go map, which has
// no order, and the walk over the values has it.
func keyOrder(ctx context.Context, cfg goconfig.Config) (map[string][]string, error) {
	order := make(map[string][]string)

	values, err := cfg.Walk(ctx, nil, -1)
	if err != nil {
		if errors.Is(err, goconfig.ErrPathNotFound) {
			return order, nil
		}

		return nil, fmt.Errorf("walk: %w", err)
	}

	seen := make(map[string]bool)

	for value := range values {
		key := value.Meta().Key

		for depth := range key {
			child := orderKey(key[:depth+1])
			if seen[child] {
				continue
			}

			seen[child] = true

			parent := orderKey(key[:depth])

			order[parent] = append(order[parent], key[depth])
		}
	}

	return order, nil
}

// orderedKeys returns the keys of values, the map at path, in the order of
// order, and those order does not know sorted after them.
func orderedKeys(order map[string][]string, path goconfig.KeyPath, values map[string]any) []string {
	keys := make([]string, 0, len(values))
	listed := make(map[string]bool, len(values))

	for _, key := range order[orderKey(path)] {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
			listed[key] = true
		}
	}

	var rest []string

	for key := range values {
		if !listed[key] {
			rest = append(rest, key)
		}
	}

	slices.Sort(rest)

	return append(keys, rest...)
}

// orderKey is the key of path in the map keyOrder returns. A key of a config
// may hold a slash, so the segments are joined with a byte no key holds.
func orderKey(path goconfig.KeyPath) string {
	return strings.Join(path, "\x00")
}
