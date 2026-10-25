package state_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// TestParseScope covers the accepted values and the default.
func TestParseScope(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]state.Scope{
		"":        state.ScopeProject,
		"project": state.ScopeProject,
		"user":    state.ScopeUser,
		"system":  state.ScopeSystem,
	} {
		got, err := state.ParseScope(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
}

// TestParseScopeRejectsUnknown pins the error for a value outside the closed
// set, and that the raw value is echoed back so the message is actionable.
func TestParseScopeRejectsUnknown(t *testing.T) {
	t.Parallel()

	_, err := state.ParseScope("global")
	require.Error(t, err)
	require.ErrorIs(t, err, state.ErrUnknownScope)
	assert.Contains(t, err.Error(), "global")
}
