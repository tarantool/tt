package install

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconcileIdenticalPins keeps the trivial case: when every package locked
// the same version, that version is chosen and constraints never matter.
func TestReconcileIdenticalPins(t *testing.T) {
	t.Parallel()

	got, err := reconcile("luasocket", []contribution{
		{pkg: "router", pin: "3.0.4", constraint: ">=3.0.0"},
		{pkg: "storage", pin: "3.0.4", constraint: ">=3.0.0,<4.0.0"},
	})
	require.NoError(t, err)
	assert.Equal(t, "3.0.4", got)
}

// TestReconcileHighestCompatible pins the core rule: among divergent pins, the
// highest that satisfies every declared constraint wins.
func TestReconcileHighestCompatible(t *testing.T) {
	t.Parallel()

	got, err := reconcile("luasocket", []contribution{
		{pkg: "router", pin: "3.0.4", constraint: ">=3.0.0"},
		{pkg: "storage", pin: "3.1.0", constraint: ">=3.0.0,<4.0.0"},
	})
	require.NoError(t, err)
	assert.Equal(t, "3.1.0", got)
}

// TestReconcileConstraintExcludesHighest covers a constraint that rules the
// highest pin out, so the next-highest satisfying pin is chosen instead.
func TestReconcileConstraintExcludesHighest(t *testing.T) {
	t.Parallel()

	got, err := reconcile("luasocket", []contribution{
		{pkg: "router", pin: "3.0.4", constraint: ">=3.0.0"},
		{pkg: "storage", pin: "3.1.0", constraint: "<3.1.0"},
	})
	require.NoError(t, err)
	assert.Equal(t, "3.0.4", got, "3.1.0 is excluded by storage's <3.1.0")
}

// TestReconcileIncompatible covers constraints that cannot be jointly satisfied
// by any locked version: a hard error with a breakdown, carrying exit code 1.
func TestReconcileIncompatible(t *testing.T) {
	t.Parallel()

	_, err := reconcile("luasocket", []contribution{
		{pkg: "router", pin: "3.0.4", constraint: "<3.1.0"},
		{pkg: "storage", pin: "3.1.0", constraint: ">=3.1.0"},
	})
	require.ErrorIs(t, err, errIncompatibleDeps)

	var exit *ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, exitStateError, exit.Code)

	assert.Contains(t, err.Error(), "router pinned 3.0.4")
	assert.Contains(t, err.Error(), "storage pinned 3.1.0")
	assert.Contains(t, err.Error(), "requires")
}

// TestReconcileTransitiveNoConstraint covers a purely transitive shared
// dependency: no package declares a constraint, so the highest pin wins.
func TestReconcileTransitiveNoConstraint(t *testing.T) {
	t.Parallel()

	got, err := reconcile("inspect", []contribution{
		{pkg: "router", pin: "3.1.2"},
		{pkg: "storage", pin: "3.1.3"},
	})
	require.NoError(t, err)
	assert.Equal(t, "3.1.3", got)
}

// TestReconcileOneConstraintDrivesChoice covers divergent pins where only one
// package constrains the range; the highest pin still inside that range wins.
func TestReconcileOneConstraintDrivesChoice(t *testing.T) {
	t.Parallel()

	got, err := reconcile("inspect", []contribution{
		{pkg: "router", pin: "3.1.3"},
		{pkg: "storage", pin: "3.1.2", constraint: "==3.1.2"},
	})
	require.NoError(t, err)
	assert.Equal(t, "3.1.2", got, "router's 3.1.3 is excluded by storage's ==3.1.2")
}
