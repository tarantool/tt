package aeon

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/aeon/pb"
	"github.com/tarantool/tt/cli/console"
	"github.com/tarantool/tt/cli/formatter"
)

func TestResultType_Format(t *testing.T) {
	tests := []struct {
		name string
		data resultType
		f    console.Format
		want string
	}{
		{
			name: "Table with string values",
			data: resultType{
				names: []string{"field1", "field2"},
				rows: []resultRow{
					{"value11", "value12"},
					{"value21", "value22"},
				},
			},
			f: console.FormatAsTable(),
			want: `+---------+---------+
| field1  | field2  |
+---------+---------+
| value11 | value12 |
+---------+---------+
| value21 | value22 |
+---------+---------+
`,
		},
		{
			name: "Table with no string values",
			data: resultType{
				names: []string{"field1", "field2"},
				rows: []resultRow{
					{[]bool{true, false}, 123},
					{nil, 456.78},
				},
			},
			f: console.FormatAsTable(),
			want: `+----------------+--------+
| field1         | field2 |
+----------------+--------+
| ["true false"] | 123    |
+----------------+--------+
| <nil>          | 456.78 |
+----------------+--------+
`,
		},
		{
			name: "Format as Yaml",
			data: resultType{
				names: []string{"field1", "field2"},
				rows: []resultRow{
					{"value11", "value12"},
					{true, 3.14},
				},
			},
			f: console.Format{
				Mode: formatter.YamlFormat,
			},
			want: `---
- field1: value11
  field2: value12
- field1: true
  field2: 3.14

`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.data.Format(tt.f)
			require.NoError(t, err)
			if got != tt.want {
				t.Errorf("ResultType.Format() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResultError_Format(t *testing.T) {
	type fields struct {
		name string
		msg  string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name:   "Name and Message",
			fields: fields{"Name of error", "Long error message string."},
			want: `---
Error: Name of error
"Long error message string."`,
		},
		{
			name:   "No message",
			fields: fields{"Name of error", ""},
			want: `---
Error: Name of error
""`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := resultError{&pb.Error{
				Name: tt.fields.name,
				Msg:  tt.fields.msg}}
			got, err := e.Format(console.Format{})
			require.NoError(t, err)
			if got != tt.want {
				t.Errorf("ResultError.Format() = %v, want %v", got, tt.want)
			}
		})
	}
}
