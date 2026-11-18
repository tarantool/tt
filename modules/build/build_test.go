package build_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/build"
)

func TestNew(t *testing.T) {
	t.Parallel()

	// The constructor uses none of the services.
	module := build.New(nil)

	assert.Empty(t, module.Commands)
	assert.Empty(t, module.FileChecks)
	require.Len(t, module.Backends, len(build.Backends()))

	names := make([]string, 0, len(module.Backends))

	for _, backend := range module.Backends {
		names = append(names, backend.Name)
		// make and shell alone run hooks.
		assert.Equal(t, backend.Name == "make" || backend.Name == "shell", backend.Hook,
			backend.Name)

		fields, err := backend.Fields()
		require.NoError(t, err, backend.Name)
		assert.NotEmpty(t, fields, backend.Name)
	}

	assert.Equal(t, []string{"make", "shell", "c", "cargo", "cmake", "go", "lua-c", "zig"}, names)
}

// TestCross: the backends that build for another platform than the machine
// tt runs on, with tt package build --target.
func TestCross(t *testing.T) {
	t.Parallel()

	var cross []string

	for _, backend := range build.Backends() {
		if backend.Cross {
			cross = append(cross, backend.Name)
		}
	}

	assert.Equal(t, []string{"make", "shell", "cargo"}, cross)
}
