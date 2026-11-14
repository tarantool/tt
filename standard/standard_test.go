package standard_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/standard"
)

func TestModules(t *testing.T) {
	t.Parallel()

	modules := standard.Modules()
	assert.NotEmpty(t, modules)
	assert.NotContains(t, modules, "builtin", "builtin is the core's, added by the caller")

	for name, ctor := range modules {
		assert.NotNil(t, ctor, name)
	}

	// Each call is a map of its own: a caller deletes from it.
	delete(modules, "aeon")
	assert.Contains(t, standard.Modules(), "aeon")
}
