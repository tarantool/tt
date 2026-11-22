package connector

import (
	"bytes"
	"encoding/base64"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
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

// TestReadLargeAnswer reads the answer of the console to an eval that
// returns a mebibyte of every byte value, as the console writes it, and the
// value it carries comes out byte for byte. The read is linear in the size of
// the answer: it was quadratic, and this one took minutes, past the deadline
// of the read.
func TestReadLargeAnswer(t *testing.T) {
	every := make([]byte, 256)
	for i := range every {
		every[i] = byte(i)
	}

	value := string(bytes.Repeat(every, 4096))

	encoded, err := msgpack.Marshal([]any{value})
	require.NoError(t, err)

	answer := "---\n- data_enc: " + base64.StdEncoding.EncodeToString(encoded) + "\n...\n"

	client, server := net.Pipe()

	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	go func() { _, _ = server.Write([]byte(answer)) }()

	data, err := readFromPlainTextConn(client, EvalPlainTextOpts{
		ReadTimeout: 30 * time.Second, PushCallback: nil, ResData: nil,
	})
	require.NoError(t, err)
	require.Equal(t, answer, string(data))

	result, err := processEvalTarantoolRes(data, nil)
	require.NoError(t, err)
	require.Equal(t, []any{value}, result)
}

// TestReadAnswerEnds pins where an answer of the console ends: after the end
// of a YAML document, after the ";" of the Lua output, and after the value of
// a push and the answer that follows it.
func TestReadAnswerEnds(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		stream string
		want   string
		pushed []any
	}{
		{"yaml", "---\n- 1\n...\n---\n- 2\n...\n", "---\n- 1\n...\n", nil},
		{"yaml with a dot line inside", "---\n- '\n\n  ...\n\n  '\n...\n",
			"---\n- '\n\n  ...\n\n  '\n...\n", nil},
		{"lua", "1;2;", "1;", nil},
		{"push", pushTag + "--- xx\n...\n---\n- true\n...\n", "---\n- true\n...\n",
			[]any{"xx"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client, server := net.Pipe()

			t.Cleanup(func() {
				_ = client.Close()
				_ = server.Close()
			})

			go func() { _, _ = server.Write([]byte(testCase.stream)) }()

			var pushed []any

			data, err := readFromPlainTextConn(client, EvalPlainTextOpts{
				ReadTimeout:  5 * time.Second,
				PushCallback: func(value any) { pushed = append(pushed, value) },
				ResData:      nil,
			})
			require.NoError(t, err)
			assert.Equal(t, testCase.want, string(data))
			assert.Equal(t, testCase.pushed, pushed)
		})
	}
}
