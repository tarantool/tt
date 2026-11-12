package emmylua

import "testing"

// SetFmtArgBudget makes fmtArgBudget budget for the test tb, and restores
// it after.
func SetFmtArgBudget(tb testing.TB, budget int) {
	tb.Helper()

	old := fmtArgBudget

	fmtArgBudget = budget

	tb.Cleanup(func() { fmtArgBudget = old })
}
