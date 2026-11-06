package data

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/printing"
)

// The vinyl runs of testdata/vinyl, written by Tarantool 3.8: space 700 has
// defer_deletes, a primary index and a secondary one of 64-byte pages; space
// 701 has a primary index only, so its UPSERTs reach the runs.
const (
	vinylDir = "testdata/vinyl"
	// A dump of the primary index of 700: a REPLACE and a DELETE that
	// deferred the DELETE of the old tuple to compaction, and an INSERT.
	runDeferred = vinylDir + "/700/0/00000000000000000017.run"
	// The deferred DELETEs compaction of that index wrote to the secondary
	// index.
	runSkipRead = vinylDir + "/700/1/00000000000000000024.run"
	// A run of the secondary index of two pages.
	runPages = vinylDir + "/700/1/00000000000000000015.run"
	// Two UPSERTs of a key squashed into one, and an UPSERT of a key no run
	// holds.
	runUpserts = vinylDir + "/701/0/00000000000000000014.run"
	// An UPSERT and a DELETE.
	runDelete = vinylDir + "/701/0/00000000000000000030.run"
)

// index is the .index file of a run.
func index(run string) string {
	return strings.TrimSuffix(run, extRun) + extIndex
}

func TestCollectRuns(t *testing.T) {
	t.Parallel()

	// A directory gives no runs without -r: they are in the directories of
	// their LSM trees.
	_, err := Collect([]string{vinylDir}, CollectOptions{Recursive: false, Runs: true})
	require.ErrorIs(t, err, ErrNoJournals)

	files, err := Collect([]string{vinylDir}, CollectOptions{Recursive: true, Runs: true})
	require.NoError(t, err)

	root, err := filepath.Abs(vinylDir)
	require.NoError(t, err)

	got := make([]string, 0, len(files))

	for _, file := range files {
		rel, err := filepath.Rel(root, file.Path)
		require.NoError(t, err)

		got = append(got, filepath.ToSlash(rel))
	}

	// By space, index and run, the index of a run before the run.
	assert.Equal(t, []string{
		"700/0/00000000000000000017.index", "700/0/00000000000000000017.run",
		"700/1/00000000000000000015.index", "700/1/00000000000000000015.run",
		"700/1/00000000000000000024.index", "700/1/00000000000000000024.run",
		"701/0/00000000000000000014.index", "701/0/00000000000000000014.run",
		"701/0/00000000000000000030.index", "701/0/00000000000000000030.run",
	}, got)
	assert.Equal(t, &LSM{SpaceID: 700, IndexID: 1, RunID: 24}, files[5].LSM)
	assert.Equal(t, KindIndex, files[4].Kind)
	assert.Equal(t, KindRun, files[5].Kind)

	// The directory of an LSM tree gives its runs.
	files, err = Collect([]string{filepath.Dir(runUpserts)},
		CollectOptions{Recursive: false, Runs: true})
	require.NoError(t, err)
	assert.Len(t, files, 4)

	// Without Runs a directory gives none, and a run named is an error.
	_, err = Collect([]string{vinylDir}, CollectOptions{Recursive: true, Runs: false})
	require.ErrorIs(t, err, ErrNoJournals)

	for _, path := range []string{runUpserts, index(runUpserts)} {
		_, err = Collect([]string{path}, CollectOptions{Recursive: false, Runs: false})
		require.ErrorIs(t, err, ErrVinylRun)
		require.ErrorContains(t, err, "read it with tt data cat")
	}

	_, err = Collect([]string{"cat.go"}, CollectOptions{Recursive: false, Runs: true})
	require.ErrorIs(t, err, ErrNotJournal)
	require.ErrorContains(t, err, "want a .snap, .xlog, .vylog, .run or .index file")
}

func TestCatRunYAML(t *testing.T) {
	t.Parallel()

	filter := Filter{ShowSystem: true}

	got := cat(t, plan(t, &filter, index(runUpserts), runUpserts), &filter, printing.FormatYAML)
	assert.Equal(t, `---
HEADER:
  type: RUNINFO
BODY:
  min_key: [1]
  max_key: [2]
  min_lsn: 15
  max_lsn: 16
  page_count: 1
  stmt_stat: {insert: 0, replace: 0, delete: 0, upsert: 2}
  bloom_filter_version: 3
---
HEADER:
  type: PAGEINFO
BODY:
  offset: 88
  size: 82
  unpacked_size: 63
  row_count: 2
  min_key: [1]
  row_index_offset: 48
---
HEADER:
  type: UPSERT
  lsn: 15
BODY:
  space_id: 701
  index_id: 0
  tuple: [1, 0]
  operations: [[[+, 1, 1]], [[+, 1, 10], [=, 2, x]]]
---
HEADER:
  type: UPSERT
  lsn: 16
BODY:
  space_id: 701
  index_id: 0
  tuple: [2, 0]
  operations: [[[+, 1, 1]]]
---
HEADER:
  type: ROWINDEX
BODY:
  offsets: [0, 30]
`, got)
}

func TestCatRunFlags(t *testing.T) {
	t.Parallel()

	filter := Filter{}

	got := cat(t, plan(t, &filter, runDeferred, runSkipRead), &filter, output.FormatJSON)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	require.Len(t, lines, 5)

	assert.JSONEq(t, `{"HEADER":{"type":"REPLACE","lsn":11},"BODY":{"space_id":700,`+
		`"index_id":0,"tuple":[1,"a2"],"flags":["deferred_delete"]}}`, lines[0])
	assert.JSONEq(t, `{"HEADER":{"type":"DELETE","lsn":12},"BODY":{"space_id":700,`+
		`"index_id":0,"key":[2],"flags":["deferred_delete"]}}`, lines[1])
	assert.Contains(t, lines[2], `{"HEADER":{"type":"INSERT","lsn":13},"BODY":{"space_id":700,`+
		`"index_id":0,"tuple":[5,"e","zzz`)
	// A key of a secondary index is its parts and those of the primary one.
	assert.JSONEq(t, `{"HEADER":{"type":"DELETE","lsn":11},"BODY":{"space_id":700,`+
		`"index_id":1,"key":["a",1],"flags":["skip_read"]}}`, lines[3])
	assert.JSONEq(t, `{"HEADER":{"type":"DELETE","lsn":12},"BODY":{"space_id":700,`+
		`"index_id":1,"key":["b",2],"flags":["skip_read"]}}`, lines[4])
}

func TestCatRunLua(t *testing.T) {
	t.Parallel()

	filter := Filter{ShowSystem: true}

	// A run of a secondary index and an index file have no Lua.
	got := cat(t, plan(t, &filter, index(runUpserts), runUpserts, index(runDelete), runDelete,
		runPages, runSkipRead), &filter, FormatLua)
	assert.Equal(t, `box.space[701]:upsert({1, 0}, {{'+', 2, 1}})
box.space[701]:upsert({1, 0}, {{'+', 2, 10}, {'=', 3, 'x'}})
box.space[701]:upsert({2, 0}, {{'+', 2, 1}})
box.space[701]:upsert({1, 0}, {{'+', 2, 100}})
box.space[701]:delete({2})
`, got)

	got = cat(t, plan(t, &filter, runDeferred), &filter, FormatLua)
	assert.True(t, strings.HasPrefix(got, `box.space[700]:replace({1, 'a2'})
box.space[700]:delete({2})
box.space[700]:insert({5, 'e', 'zzz`), got)
}

func TestCatRunFilters(t *testing.T) {
	t.Parallel()

	statements := func(filter *Filter, paths ...string) []string {
		t.Helper()

		txns := scan(t, plan(t, filter, paths...), filter)
		out := make([]string, 0, len(txns))

		for _, txn := range txns {
			out = append(out, txn.file+"@"+strings.Join(txn.rows, ","))
		}

		return out
	}

	all := []string{runUpserts, runDelete}

	// The bounds apply to the LSNs of the statements; the rows of an index
	// file are kept whole. A row index changes no space: it is kept with
	// --show-system only.
	assert.Equal(t, []string{
		"00000000000000000014.index@0:0", "00000000000000000014.index@0:0",
		"00000000000000000014.run@0:16",
	}, statements(&Filter{From: 16, To: 16, HasFrom: true, HasTo: true},
		index(runUpserts), runUpserts))
	assert.Equal(t, []string{"00000000000000000014.run@0:15", "00000000000000000014.run@0:16"},
		statements(&Filter{ToVClock: map[uint32]int64{1: 10, 2: 6}}, all...))
	assert.Equal(t, []string{"00000000000000000030.run@0:21"},
		statements(&Filter{FromVClock: map[uint32]int64{1: 20}}, all...))
	assert.Equal(t, []string{
		"00000000000000000014.run@0:15", "00000000000000000014.run@0:16",
		"00000000000000000014.run@0:0",
	}, statements(&Filter{ShowSystem: true}, runUpserts))

	// --space keeps the runs of the space their path names; the replicas
	// do not apply to a run.
	assert.Empty(t, statements(&Filter{Spaces: []uint32{700}}, all...))
	assert.Len(t, statements(&Filter{Spaces: []uint32{701}, Replicas: []uint32{1}}, all...), 4)

	// A time leaves the runs out: their statements have none.
	assert.Empty(t, plan(t, &Filter{Before: 4102444800}, all...).Files)
}

func TestCatRunOutsideLSM(t *testing.T) {
	t.Parallel()

	// A run whose path names no space and index.
	data, err := os.ReadFile(runUpserts)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "00000000000000000014.run")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	filter := Filter{}
	selected := plan(t, &filter, path)
	require.Nil(t, selected.Files[0].LSM)

	got := cat(t, selected, &filter, output.FormatJSON)
	assert.JSONEq(t, `{"HEADER":{"type":"UPSERT","lsn":15},"BODY":{"tuple":[1,0],`+
		`"operations":[[["+",1,1]],[["+",1,10],["=",2,"x"]]]}}`,
		strings.Split(got, "\n")[0])

	// It has no Lua, and no space --space names.
	assert.Empty(t, cat(t, selected, &filter, FormatLua))
	assert.Empty(t, scan(t, selected, &Filter{Spaces: []uint32{701}}))
}
