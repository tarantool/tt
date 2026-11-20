package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/sdk/v3"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// TestRegistryFlagUsage: the help of --registry, which the SDK holds for the
// modules, names the variable the core reads the rock servers from.
func TestRegistryFlagUsage(t *testing.T) {
	t.Parallel()

	assert.Contains(t, sdk.RegistryFlagUsage, manifestrocks.EnvRegistries+" ")
}
