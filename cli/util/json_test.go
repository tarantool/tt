package util_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/util"
)

func TestMarshalJSON(t *testing.T) {
	t.Parallel()

	value := map[string]any{"constraint": ">=3.0.0 & <4", "tag": "<b>"}

	compact, err := util.MarshalJSON(value, "")
	require.NoError(t, err)
	assert.JSONEq(t, `{"constraint": ">=3.0.0 & <4", "tag": "<b>"}`, string(compact))
	assert.NotContains(t, string(compact), `\u00`, "no HTML escapes")
	assert.False(t, strings.HasSuffix(string(compact), "\n"), "no newline, as json.Marshal")

	indented, err := util.MarshalJSON(value, "  ")
	require.NoError(t, err)

	expected, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	assert.JSONEq(t, string(expected), string(indented))
	assert.Contains(t, string(indented), "\n  \"constraint\": \">=3.0.0 & <4\",\n")

	_, err = util.MarshalJSON(func() {}, "")
	require.Error(t, err)
}
