package coredump

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGDBToolsCommit checks that the commit the help of inspect names is
// the one vendor.json of the extensions of tarantool-gdb-tools records,
// abbreviated.
func TestGDBToolsCommit(t *testing.T) {
	t.Parallel()

	record := readVendorRecordOf(t, gdbToolsSet)
	require.NotEmpty(t, record.Files)

	commit, err := gdbToolsSet.vendorCommit()
	require.NoError(t, err)
	assert.Equal(t, record.Files[0].Commit, commit)
	assert.Equal(t, commit[:shortCommit], GDBToolsCommit())
	assert.Len(t, GDBToolsCommit(), shortCommit)
}

// TestVendorCommit checks that the commit of a set is the one its
// vendor.json records for all its files, and that a record of no file, of
// files of other commits or of a commit that is not full names none.
func TestVendorCommit(t *testing.T) {
	t.Parallel()

	const (
		one   = "146e82b083968df95aff89b9c6ef88fa844a0368"
		other = "8af934d93cf26fb1e9b08b28a8b51833d55fc8e8"
	)

	set := func(record string) extensionSet {
		files := fstest.MapFS{}
		if record != "" {
			files["set/"+vendorFile] = &fstest.MapFile{Data: []byte(record)}
		}

		return extensionSet{files: files, dir: "set"}
	}

	commit, err := set(`{"files": [{"file": "a.py", "commit": "` + one + `"},
		{"file": "b.py", "commit": "` + one + `"}]}`).vendorCommit()
	require.NoError(t, err)
	assert.Equal(t, one, commit)

	cases := []struct {
		name    string
		record  string
		err     error
		message string
	}{
		{
			name: "two commits",
			record: `{"files": [{"file": "a.py", "commit": "` + one + `"},
				{"file": "b.py", "commit": "` + other + `"}]}`,
			err:     errVendorCommit,
			message: "set/vendor.json: no one commit: b.py",
		},
		{
			name:    "an abbreviated commit",
			record:  `{"files": [{"file": "a.py", "commit": "146e82b0"}]}`,
			err:     errVendorCommit,
			message: "set/vendor.json: no one commit: a.py",
		},
		{
			name:    "no file",
			record:  `{"files": []}`,
			err:     errVendorCommit,
			message: "set/vendor.json: no one commit: it lists no file",
		},
		{
			name:    "no JSON",
			record:  `{"files": [`,
			err:     nil,
			message: "cannot read set/vendor.json",
		},
		{
			name:    "no record",
			record:  "",
			err:     fs.ErrNotExist,
			message: "cannot read the record of the embedded GDB-extensions",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			commit, err := set(testCase.record).vendorCommit()
			require.Error(t, err)
			assert.Empty(t, commit)

			if testCase.err != nil {
				require.ErrorIs(t, err, testCase.err)
			}

			assert.ErrorContains(t, err, testCase.message)
		})
	}
}
