package sdk_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/sdk"
)

// TestDependencyMoveString pins the lines tt package and the modules report
// the moves of a lock with.
func TestDependencyMoveString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a 1.0.0-1", sdk.DependencyMove{Name: "a", From: "", To: "1.0.0-1"}.String())
	assert.Equal(t, "a 1.0.0-1 -> (dropped)",
		sdk.DependencyMove{Name: "a", From: "1.0.0-1", To: ""}.String())
	assert.Equal(t, "a 1.0.0-1 -> 1.1.0-1",
		sdk.DependencyMove{Name: "a", From: "1.0.0-1", To: "1.1.0-1"}.String())
}
