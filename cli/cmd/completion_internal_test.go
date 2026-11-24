package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequireBashCompletion checks that the bash completion cobra writes
// gets the check for bash-completion, and that a script without the
// fallback it replaces is an error rather than a script without the check.
func TestRequireBashCompletion(t *testing.T) {
	var script bytes.Buffer

	require.NoError(t, (&cobra.Command{Use: "tt"}).GenBashCompletionV2(&script, true))

	guarded, err := requireBashCompletion(script.Bytes(), "tt")
	require.NoError(t, err)
	assert.Contains(t, string(guarded),
		"if ! declare -F _get_comp_words_by_ref >/dev/null 2>&1; then")
	assert.Contains(t, string(guarded), "tt: completion needs the bash-completion package:")
	assert.Equal(t, 1, bytes.Count(guarded, []byte("__tt_no_bash_completion=1")))

	_, err = requireBashCompletion([]byte("# another script\n"), "tt")
	require.ErrorIs(t, err, errBashCompletionGuard)
}
