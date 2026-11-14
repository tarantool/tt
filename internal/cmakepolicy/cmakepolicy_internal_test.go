package cmakepolicy

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const entry = Variable + "=" + Version

func TestEnvironAddsPolicyVersion(t *testing.T) {
	t.Parallel()

	environ := []string{"HOME=/home/user", "PATH=/bin"}

	got := Environ(environ)

	assert.Equal(t, []string{"HOME=/home/user", "PATH=/bin", entry}, got)
	assert.Equal(t, []string{"HOME=/home/user", "PATH=/bin"}, environ, "environ is not changed")

	// The entry goes to a slice of its own, not into the spare capacity of
	// environ, where another append would overwrite it.
	spare := append(make([]string, 0, 2), "PATH=/bin")

	got = Environ(spare)
	spare = append(spare, "OTHER=1")

	assert.Equal(t, []string{"PATH=/bin", entry}, got)
	assert.Equal(t, []string{"PATH=/bin", "OTHER=1"}, spare)
}

func TestEnvironKeepsUsersPolicyVersion(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"3.10", Version, ""} {
		environ := []string{"PATH=/bin", Variable + "=" + value}

		assert.Equal(t, environ, Environ(environ), "the user's %q is kept", value)
	}

	// A variable named alike is not the one.
	assert.Equal(t, []string{Variable + "_X=1", entry}, Environ([]string{Variable + "_X=1"}))
}

// unsetVariable unsets Variable for the test and clears what Setenv
// remembers, restoring both after it.
func unsetVariable(t *testing.T) {
	t.Helper()

	t.Setenv(Variable, "")
	require.NoError(t, os.Unsetenv(Variable))

	was := defaulted.Load()
	defaulted.Store(false)
	t.Cleanup(func() { defaulted.Store(was) })
}

func TestSetenvSetsPolicyVersion(t *testing.T) {
	unsetVariable(t)

	environ := []string{"PATH=/bin", entry}
	assert.Equal(t, environ, Strip(environ), "nothing of tt to strip before Setenv")

	require.NoError(t, Setenv())
	assert.Equal(t, Version, os.Getenv(Variable))

	// The second call finds it set and keeps it.
	require.NoError(t, Setenv())
	assert.Equal(t, Version, os.Getenv(Variable))

	assert.Equal(t, []string{"PATH=/bin"}, Strip(environ))
	assert.Equal(t, []string{"PATH=/bin", entry}, environ, "environ is not changed")

	// What is not the value of tt is the user's.
	for _, user := range []string{"3.10", ""} {
		environ := []string{"PATH=/bin", Variable + "=" + user}

		assert.Equal(t, environ, Strip(environ), "the user's %q is kept", user)
	}
}

func TestSetenvKeepsUsersPolicyVersion(t *testing.T) {
	for _, user := range []string{"3.10", Version, ""} {
		t.Run(user, func(t *testing.T) {
			unsetVariable(t)
			t.Setenv(Variable, user)

			require.NoError(t, Setenv())
			assert.Equal(t, user, os.Getenv(Variable))

			environ := []string{"PATH=/bin", Variable + "=" + user}
			assert.Equal(t, environ, Strip(environ), "the user's %q is kept", user)
		})
	}
}
