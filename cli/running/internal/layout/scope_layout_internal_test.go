package layout

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopeLayout(t *testing.T) {
	scopeLayout, err := NewScopeLayout("/opt/tarantool/", "myapp", "storage-1")
	require.NoError(t, err)

	assert.Equal(t, "/opt/tarantool/var/lib/myapp/storage-1", scopeLayout.DataDir())
	assert.Equal(t, "/opt/tarantool/var/log/myapp/storage-1", scopeLayout.LogDir())
	assert.Equal(t, "/opt/tarantool/var/run/myapp/storage-1", scopeLayout.RunDir())
	assert.Equal(t, "/opt/tarantool/var/log/myapp/storage-1/tt.log", scopeLayout.LogFile())
	assert.Equal(t, "/opt/tarantool/var/run/myapp/storage-1/tt.pid", scopeLayout.PidFile())
	assert.Equal(t, "/opt/tarantool/var/run/myapp/storage-1/tarantool.control",
		scopeLayout.ConsoleSocket())
	assert.Equal(t, "/opt/tarantool/var/run/myapp/storage-1/tarantool.sock",
		scopeLayout.BinaryPort())
}

// TestScopeLayoutSameNamesAsMultiInstLayout pins that the scope layout moves
// the files of an instance without renaming them.
func TestScopeLayoutSameNamesAsMultiInstLayout(t *testing.T) {
	scopeLayout, err := NewScopeLayout("/root", "app", "inst")
	require.NoError(t, err)

	multiLayout, err := NewMultiInstLayout("/base", "app", "inst")
	require.NoError(t, err)

	for _, pair := range [...][2]string{
		{scopeLayout.PidFile(), multiLayout.PidFile("/var/run")},
		{scopeLayout.LogFile(), multiLayout.LogFile("/var/log")},
		{scopeLayout.ConsoleSocket(), multiLayout.ConsoleSocket("/var/run")},
		{scopeLayout.BinaryPort(), multiLayout.BinaryPort("/var/run")},
	} {
		assert.Equal(t, "/root"+pair[1], pair[0])
	}
}

func TestScopeLayoutErrors(t *testing.T) {
	for name, args := range map[string][3]string{
		"relative root":        {"var", "app", "inst"},
		"empty root":           {"", "app", "inst"},
		"empty application":    {"/root", "", "inst"},
		"empty instance":       {"/root", "app", ""},
		"dot application":      {"/root", ".", "inst"},
		"dot-dot instance":     {"/root", "app", ".."},
		"slash in instance":    {"/root", "app", "a/b"},
		"slash in application": {"/root", "../x", "inst"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewScopeLayout(args[0], args[1], args[2])
			assert.Error(t, err)
		})
	}
}
