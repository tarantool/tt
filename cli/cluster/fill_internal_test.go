package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"
	sdkcluster "github.com/tarantool/tt/sdk/cluster/v3"
)

// fillFrom fills the config of the YAML dstYAML with that of srcYAML and
// returns the result as Go values.
func fillFrom(t *testing.T, dstYAML, srcYAML string) map[string]any {
	t.Helper()

	dst, err := BuildMutableFromBytes(t.Context(), []byte(dstYAML))
	require.NoError(t, err)

	src, err := BuildGoConfigFromBytes(t.Context(), []byte(srcYAML))
	require.NoError(t, err)

	require.NoError(t, fillOnlyMerge(t.Context(), dst, src))

	var got map[string]any

	_, err = dst.Get(nil, &got)
	require.NoError(t, err)

	return got
}

func TestFillOnlyMergeArrayOfSourceOnly(t *testing.T) {
	t.Parallel()

	// The array only the storage has comes over as an array, not as the map
	// {"0": {uri: ...}} its items would make set one by one.
	got := fillFrom(t, "config:\n  etcd:\n    prefix: /app\n", `groups:
  g:
    replicasets:
      r:
        instances:
          i:
            iproto:
              listen:
                - uri: 127.0.0.1:3301
                - uri: 127.0.0.1:3302
`)

	assert.Equal(t, map[string]any{
		"config": map[string]any{"etcd": map[string]any{"prefix": "/app"}},
		"groups": map[string]any{"g": map[string]any{"replicasets": map[string]any{
			"r": map[string]any{"instances": map[string]any{"i": map[string]any{
				"iproto": map[string]any{"listen": []any{
					map[string]any{"uri": "127.0.0.1:3301"},
					map[string]any{"uri": "127.0.0.1:3302"},
				}},
			}}},
		}}},
	}, got)
}

func TestFillOnlyMergeArrayIsWhole(t *testing.T) {
	t.Parallel()

	// An array of dst takes nothing of a longer one of src.
	got := fillFrom(t, "roles: [a]\nlisten:\n  - uri: x\n",
		"roles: [b, c, d]\nlisten:\n  - uri: y\n    params: {transport: ssl}\n  - uri: z\n")

	assert.Equal(t, map[string]any{
		"roles":  []any{"a"},
		"listen": []any{map[string]any{"uri": "x"}},
	}, got)
}

func TestFillOnlyMergeMaps(t *testing.T) {
	t.Parallel()

	got := fillFrom(t, `labels:
  a: file
empty: {}
scalar: 1
nothing: null
`, `labels:
  a: storage
  b: storage
empty:
  c: storage
scalar:
  d: storage
nothing:
  e: storage
`)

	assert.Equal(t, map[string]any{
		// Maps merge key by key, the file first.
		"labels": map[string]any{"a": "file", "b": "storage"},
		// An empty map has no key to keep.
		"empty": map[string]any{"c": "storage"},
		// A value of another kind than a map stays.
		"scalar": int64(1),
		// Null holds nothing.
		"nothing": map[string]any{"e": "storage"},
	}, got)
}

func TestFillOnlyMergeEmptyMapOfSource(t *testing.T) {
	t.Parallel()

	got := fillFrom(t, "labels:\n  a: x\nroles: [r]\n", "labels: {}\nroles: {}\n")

	assert.Equal(t, map[string]any{
		"labels": map[string]any{"a": "x"},
		"roles":  []any{"r"},
	}, got)
}

func TestFillOnlyMergeKeepsOrderOfSource(t *testing.T) {
	t.Parallel()

	dst, err := BuildMutableFromBytes(t.Context(), []byte("first: 1\n"))
	require.NoError(t, err)

	src, err := BuildGoConfigFromBytes(t.Context(),
		[]byte("zeta: 1\nalpha:\n  y: 1\n  b: 2\nmid: 3\n"))
	require.NoError(t, err)

	require.NoError(t, fillOnlyMerge(t.Context(), dst, src))

	snap := dst.Snapshot()

	out, err := snap.MarshalYAML()
	require.NoError(t, err)
	assert.Equal(t, "first: 1\nzeta: 1\nalpha:\n  y: 1\n  b: 2\nmid: 3\n", string(out))
}

func TestFillOnlyMergeEmptySource(t *testing.T) {
	t.Parallel()

	got := fillFrom(t, "a: 1\n", "")
	assert.Equal(t, map[string]any{"a": int64(1)}, got)

	got = fillFrom(t, "", "a: [1, 2]\n")
	assert.Equal(t, map[string]any{"a": []any{int64(1), int64(2)}}, got)
}

// fakeCollector returns the data it holds.
type fakeCollector []sdkcluster.Data

func (c fakeCollector) Collect() ([]sdkcluster.Data, error) {
	return c, nil
}

func TestCollectDataBytesKeepsArrays(t *testing.T) {
	t.Parallel()

	// The keys of a prefix merge as the sources of a config do: the first
	// one wins, and an array of a later one comes over whole.
	data, err := CollectDataBytes(context.Background(), fakeCollector{
		{Source: "/prefix/config/a", Value: []byte("roles: [a]\n"), Revision: 1},
		{
			Source:   "/prefix/config/b",
			Value:    []byte("roles: [b, c]\niproto:\n  listen:\n    - uri: 127.0.0.1:3301\n"),
			Revision: 1,
		},
	})
	require.NoError(t, err)

	cfg, err := BuildGoConfigFromBytes(t.Context(), data)
	require.NoError(t, err)

	var got map[string]any

	_, err = cfg.Get(goconfig.NewKeyPath(""), &got)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"roles":  []any{"a"},
		"iproto": map[string]any{"listen": []any{map[string]any{"uri": "127.0.0.1:3301"}}},
	}, got)
}

// TestFillOnlyMergeEmptyMapOfSourceOnly checks that an empty map only the
// storage has comes over as an empty map.
func TestFillOnlyMergeEmptyMapOfSourceOnly(t *testing.T) {
	t.Parallel()

	got := fillFrom(t, "a: 1\n", "labels: {}\n")

	assert.Equal(t, map[string]any{"a": int64(1), "labels": map[string]any{}}, got)
}
