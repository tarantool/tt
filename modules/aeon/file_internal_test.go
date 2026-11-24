package aeon

import (
	"os"
	"os/user"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHomeDir checks that the home directory is $HOME, whatever the user
// database says, and the one the database has without HOME: a container may
// run the console as a uid the database does not know.
func TestHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := homeDir()
	require.NoError(t, err)
	assert.Equal(t, home, got)

	require.NoError(t, os.Unsetenv("HOME"))

	got, err = homeDir()

	current, currentErr := user.Current()
	if currentErr != nil {
		require.ErrorContains(t, err, currentErr.Error())

		return
	}

	require.NoError(t, err)
	assert.Equal(t, current.HomeDir, got)
}
