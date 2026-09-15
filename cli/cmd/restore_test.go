package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/restore"
)

// One directory spelled three ways is one directory. The report is what an
// operator checks a restore against, and listing the same place three times
// would have them looking for a split that is not there -- or, on the landing
// summary, dividing one directory's files between three lines.
func TestLayoutDirs_CountsOneDirectoryOnce(t *testing.T) {
	tests := []struct {
		name   string
		layout restore.Layout
		want   []string
	}{
		{
			name:   "three spellings of one directory",
			layout: restore.Layout{Snapshot: "/x/a", WAL: "/x/./a", Vinyl: "/x/a/"},
			want:   []string{"/x/a"},
		},
		{
			name:   "a flat layout",
			layout: restore.FlatLayout("/data"),
			want:   []string{"/data"},
		},
		{
			name:   "snapshots and journals sharing a directory",
			layout: restore.Layout{Snapshot: "/data", WAL: "/data", Vinyl: "/data/vinyl"},
			want:   []string{"/data", "/data/vinyl"},
		},
		{
			name:   "three directories of their own",
			layout: restore.Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
			want:   []string{"/memtx", "/wal", "/vinyl"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, layoutDirs(tc.layout))
		})
	}
}

// The line naming the one directory a restore wrote into names the directory
// the files are in. --work-dir is the operator's own spelling of it only when
// it is that directory: with a cluster configuration the flag names the
// directory the instance is launched from, and the data goes somewhere under
// it, so printing the flag would point at a place the restore never wrote to.
func TestNameOfFlatDir_NamesTheDirectoryTheFilesAreIn(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		name    string
		dir     string
		workDir string
		want    string
	}{
		{
			name:    "--work-dir is the directory, kept as it was typed",
			dir:     filepath.Join(cwd, "restored"),
			workDir: "restored",
			want:    "restored",
		},
		{
			name:    "--work-dir is not the directory",
			dir:     "/launch/var/lib/storage-001-a",
			workDir: "/launch",
			want:    "/launch/var/lib/storage-001-a",
		},
		{
			name: "no --work-dir",
			dir:  "/memtx",
			want: "/memtx",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, nameOfFlatDir(tc.dir, tc.workDir))
		})
	}
}

// Every file the run landed has to be counted against the directory it went
// into, whichever spelling of that directory the layout carries.
func TestCountLandedIn_CountsEverySpellingOfADirectory(t *testing.T) {
	layout := restore.Layout{Snapshot: "/x/a", WAL: "/x/./a", Vinyl: "/x/a/"}
	files := []string{
		"00000000000000000000.snap",
		"00000000000000000000.xlog",
		"00000000000000000000.vylog",
	}

	dirs := layoutDirs(layout)
	assert.Equal(t, []string{"/x/a"}, dirs)
	assert.Equal(t, len(files), countLandedIn(files, layout, dirs[0]))
}
