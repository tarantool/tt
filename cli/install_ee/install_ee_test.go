package install_ee

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type getCredsFromFileInputValue struct {
	path string
}

type getCredsFromFileOutputValue struct {
	result UserCredentials
	err    error
}

func TestGetCredsFromFile(t *testing.T) {
	assert := assert.New(t)

	testCases := make(map[getCredsFromFileInputValue]getCredsFromFileOutputValue)

	testCases[getCredsFromFileInputValue{path: "./testdata/nonexisting"}] =
		getCredsFromFileOutputValue{
			result: UserCredentials{},
			err:    fmt.Errorf("open ./testdata/nonexisting: no such file or directory"),
		}

	testCases[getCredsFromFileInputValue{path: "./testdata/creds_ok"}] =
		getCredsFromFileOutputValue{
			result: UserCredentials{
				Username: "toor",
				Password: "1234",
			},
			err: nil,
		}

	testCases[getCredsFromFileInputValue{path: "./testdata/creds_bad"}] =
		getCredsFromFileOutputValue{
			result: UserCredentials{},
			err:    fmt.Errorf("corrupted credentials"),
		}

	for input, output := range testCases {
		creds, err := getCredsFromFile(input.path)

		if output.err == nil {
			assert.Nil(err)
			assert.Equal(output.result, creds)
		} else {
			assert.Equal(output.err.Error(), err.Error())
		}
	}
}

func Test_getCredsFromEnvVars(t *testing.T) {
	tests := []struct {
		name    string
		prepare func()
		want    UserCredentials
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "Environment variables are not passed",
			prepare: func() {},
			want:    UserCredentials{Username: "", Password: ""},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				if err.Error() == "no credentials in environment variables were found" {
					return true
				}
				return false
			},
		},
		{
			name: "Environment variables are passed",
			prepare: func() {
				t.Setenv("TT_EE_USERNAME", "tt_test")
				t.Setenv("TT_EE_PASSWORD", "tt_test")
			},
			want: UserCredentials{Username: "tt_test", Password: "tt_test"},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return true
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.prepare()
			got, err := getCredsFromEnvVars()
			if !tt.wantErr(t, err, fmt.Sprintf("getCredsFromEnvVars()")) {
				return
			}
			assert.Equalf(t, tt.want, got, "getCredsFromEnvVars()")
		})
	}
}
