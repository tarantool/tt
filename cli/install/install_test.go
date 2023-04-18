package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_dirsAreWriteable(t *testing.T) {
	tmpDirNonWriteableForAll := t.TempDir()
	// dr-xr-xr-x mode.
	permissions := 0555
	require.NoError(t, os.Chmod(tmpDirNonWriteableForAll, os.FileMode(permissions)))

	tmpDirNonWriteableExceptOwner := t.TempDir()
	// drwxr-xr-x mode.
	permissions = 0755
	require.NoError(t, os.Chmod(tmpDirNonWriteableExceptOwner, os.FileMode(permissions)))

	type args struct {
		dir string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Checking access to non writeable directory",
			args: args{dir: tmpDirNonWriteableForAll},
			want: false,
		},
		{
			name: "Checking access to non writeable directory except of owner",
			args: args{dir: tmpDirNonWriteableExceptOwner},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, dirsIsWriteable(tt.args.dir),
				"dirIsWriteable(%v)", tt.args.dir)
		})
	}
}

func Test_subDirIsWritable(t *testing.T) {
	tmpDirNonWriteableForAll := t.TempDir()
	// dr-xr-xr-x mode.
	permissions := 0555
	require.NoError(t, os.Chmod(tmpDirNonWriteableForAll, os.FileMode(permissions)))

	tmpDirNonWriteableExceptOwner := t.TempDir()
	// drwxr-xr-x mode.
	permissions = 0755
	require.NoError(t, os.Chmod(tmpDirNonWriteableExceptOwner, os.FileMode(permissions)))

	type args struct {
		dir string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Subdirectory is writeable",
			args: args{dir: filepath.Join(tmpDirNonWriteableExceptOwner, "test", "test")},
			want: true,
		},
		{
			name: "Subdirectory is not writeable",
			args: args{dir: filepath.Join(tmpDirNonWriteableForAll, "test", "test")},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, subDirIsWritable(tt.args.dir), "subDirIsWritable(%v)",
				tt.args.dir)
		})
	}
}
