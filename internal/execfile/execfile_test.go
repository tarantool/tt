package execfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/execfile"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "file")

	require.NoError(t, execfile.Write(path, strings.NewReader("first, longer"), 0o750))
	require.NoError(t, execfile.WriteFile(path, []byte("second"), 0o600))

	// The file is truncated and keeps the mode it was created with, as with
	// os.WriteFile.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "second", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), info.Mode().Perm()&0o750)

	// The error of opening the file is the *PathError of os.OpenFile.
	var pathErr *os.PathError

	err = execfile.WriteFile(filepath.Join(path, "under-a-file"), nil, 0o644)
	require.ErrorAs(t, err, &pathErr)
	assert.Equal(t, "open", pathErr.Op)
}
