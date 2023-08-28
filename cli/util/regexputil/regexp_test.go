package regexputil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyVars(t *testing.T) {
	type args struct {
		templateStr string
		data        map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
		errStr  string
	}{
		{
			"No vars template",
			args{"Hello world!", map[string]string{}},
			"Hello world!",
			false,
			"",
		},
		{
			"No vars template",
			args{"Hello world!", map[string]string{
				"greeting": "Hello",
			}},
			"Hello world!",
			false,
			"",
		},
		{
			"Missing vars template",
			args{"{{ hello }} world!", map[string]string{
				"greeting": "Hello",
			}},
			"{{ hello }} world!",
			true,
			`missing vars: hello
in template string: "{{ hello }} world!"`,
		},
		{
			"All vars present",
			args{"{{ greeting    }} {{who}}!", map[string]string{
				"greeting": "Hello",
				"who":      "world",
			}},
			"Hello world!",
			false,
			"",
		},
		{
			"One var is missing",
			args{"{{ greeting    }} {{who}}!", map[string]string{
				"who": "world",
			}},
			"{{ greeting    }} world!",
			true,
			`missing vars: greeting
in template string: "{{ greeting    }} {{who}}!"`,
		},
		{
			"Both vars are missing",
			args{"{{ greeting    }} {{who}}!", map[string]string{}},
			"{{ greeting    }} {{who}}!",
			true,
			`missing vars: `,
		},
		{
			"Empty var",
			args{"{{  }} {{}}!", map[string]string{}},
			"{{  }} {{}}!",
			false,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyVars(tt.args.templateStr, tt.args.data)
			if tt.wantErr {
				assert.ErrorContainsf(t, err, tt.errStr, "")
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
