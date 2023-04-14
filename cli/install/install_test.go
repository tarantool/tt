package install

import (
	"os"
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
