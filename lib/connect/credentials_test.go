package connect

import (
	"fmt"
	"os"
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

	testCases[getCredsFromFileInputValue{path: "./testdata/nonexisting"}] = getCredsFromFileOutputValue{
		result: UserCredentials{},
		err:    fmt.Errorf("open ./testdata/nonexisting: no such file or directory"),
	}

	file, err := os.CreateTemp("/tmp", "tt-unittest-*.bat")
	assert.Nil(err)
	file.WriteString("user\npass")
	defer os.Remove(file.Name())

	testCases[getCredsFromFileInputValue{path: file.Name()}] = getCredsFromFileOutputValue{
		result: UserCredentials{
			Username: "user",
			Password: "pass",
		},
		err: nil,
	}

	file, err = os.CreateTemp("/tmp", "tt-unittest-*.bat")
	assert.Nil(err)
	file.WriteString("")
	defer os.Remove(file.Name())

	testCases[getCredsFromFileInputValue{path: file.Name()}] = getCredsFromFileOutputValue{
		result: UserCredentials{},
		err:    fmt.Errorf("login not set"),
	}

	file, err = os.CreateTemp("/tmp", "tt-unittest-*.bat")
	assert.Nil(err)
	file.WriteString("user")
	defer os.Remove(file.Name())

	testCases[getCredsFromFileInputValue{path: file.Name()}] = getCredsFromFileOutputValue{
		result: UserCredentials{},
		err:    fmt.Errorf("password not set"),
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
				t.Setenv(EnvSdkUsername, "tt_test")
				t.Setenv(EnvSdkPassword, "tt_test")
			},
			want: UserCredentials{Username: "tt_test", Password: "tt_test"},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return true
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvSdkUsername, "")
			t.Setenv(EnvSdkPassword, "")
			tt.prepare()
			got, err := getCredsFromEnvVars()
			if !tt.wantErr(t, err, fmt.Sprintf("getCredsFromEnvVars()")) {
				return
			}
			assert.Equalf(t, tt.want, got, "getCredsFromEnvVars()")
		})
	}
}
