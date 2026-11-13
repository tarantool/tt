package connector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pushTag = "%TAG !push! tag:tarantool.io/push,2018\n"

// TestGetPushedData pins the values a push decodes to: maps of any keys,
// the YAML 1.1 booleans as booleans and a timestamp as its text.
func TestGetPushedData(t *testing.T) {
	cases := []struct {
		name string
		push string
		want any
	}{
		{"string", pushTag + "--- xx\n...\n", "xx"},
		{"quoted", pushTag + "--- 'yes'\n...\n", "yes"},
		{"old bool", pushTag + "--- on\n...\n", true},
		{"timestamp", pushTag + "--- 2024-01-02\n...\n", "2024-01-02"},
		{"number", pushTag + "--- 0x1F\n...\n", 31},
		{"null", pushTag + "--- null\n...\n", nil},
		{"array", pushTag + "--- [1, 'a', off]\n...\n", []any{1, "a", false}},
		{
			"map", pushTag + "--- {1: 'a', 'b': [2, {'c': y}], 'd': 2024-01-02}\n...\n",
			map[any]any{1: "a", "b": []any{2, map[any]any{"c": true}}, "d": "2024-01-02"},
		},
		{"lua", "-- Push\n\"xx\";", "\"xx\""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := getPushedData([]byte(tc.push))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestGetPlainTextEvalResYaml pins how the YAML answer of an eval is read:
// one data_enc, an error of the server, and the answers it refuses.
func TestGetPlainTextEvalResYaml(t *testing.T) {
	data, err := getPlainTextEvalResYaml([]byte("---\n- data_enc: kqE=\n...\n"))
	require.NoError(t, err)
	assert.Equal(t, "kqE=", data)

	_, err = getPlainTextEvalResYaml([]byte("---\n- error: 'Some error'\n...\n"))
	require.EqualError(t, err, "Some error")

	_, err = getPlainTextEvalResYaml([]byte("---\n- error: yes\n...\n"))
	require.EqualError(t, err, "yes")

	_, err = getPlainTextEvalResYaml([]byte("---\n- data_enc: a\n- data_enc: b\n...\n"))
	require.EqualError(t, err, "expected one result, found 2")

	_, err = getPlainTextEvalResYaml([]byte("---\n...\n"))
	require.EqualError(t, err, "expected one result, found 0")

	_, err = getPlainTextEvalResYaml([]byte(""))
	require.EqualError(t, err, "expected one result, found 0")

	_, err = getPlainTextEvalResYaml([]byte("---\n- data_enc: a\n  other: b\n...\n"))
	require.ErrorContains(t, err, "failed to parse eval result: yaml: unmarshal errors:\n"+
		"  line 3: field other not found in type connector.PlainTextEvalRes")

	_, err = getPlainTextEvalResYaml([]byte("---\n- data_enc: a\n  data_enc: b\n...\n"))
	require.ErrorContains(t, err, "failed to parse eval result: yaml: unmarshal errors:\n"+
		"  line 3: ")

	_, err = getPlainTextEvalResYaml([]byte("---\n- error: [1, 2]\n...\n"))
	require.ErrorContains(t, err, "failed to parse eval result: yaml: unmarshal errors:\n"+
		"  line 2: field error not found in type connector.PlainTextEvalRes")
}
