package connect

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errMissingCredentialsFile = errors.New(
		"opening the credentials file: open ./testdata/nonexisting: no such file or directory",
	)
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

	testCases[getCredsFromFileInputValue{
		path: "./testdata/nonexisting",
	}] = getCredsFromFileOutputValue{
		result: UserCredentials{},
		err:    errMissingCredentialsFile,
	}

	file, err := os.CreateTemp("/tmp", "tt-unittest-*.bat")
	require.NoError(t, err)

	_, _ = file.WriteString("user\npass")

	defer func() {
		_ = os.Remove(file.Name())
	}()

	testCases[getCredsFromFileInputValue{path: file.Name()}] = getCredsFromFileOutputValue{
		result: UserCredentials{
			Username: "user",
			Password: "pass",
		},
		err: nil,
	}

	file, err = os.CreateTemp("/tmp", "tt-unittest-*.bat")
	require.NoError(t, err)

	_, _ = file.WriteString("")

	defer func() {
		_ = os.Remove(file.Name())
	}()

	testCases[getCredsFromFileInputValue{path: file.Name()}] = getCredsFromFileOutputValue{
		result: UserCredentials{},
		err:    errLoginNotSet,
	}

	file, err = os.CreateTemp("/tmp", "tt-unittest-*.bat")
	require.NoError(t, err)

	_, _ = file.WriteString("user")

	defer func() {
		_ = os.Remove(file.Name())
	}()

	testCases[getCredsFromFileInputValue{path: file.Name()}] = getCredsFromFileOutputValue{
		result: UserCredentials{},
		err:    errPasswordNotSet,
	}

	for input, output := range testCases {
		creds, err := getCredsFromFile(input.path)

		if output.err == nil {
			require.NoError(t, err)
			assert.Equal(output.result, creds)
		} else {
			assert.Equal(output.err.Error(), err.Error())
		}
	}
}

// TestGetCredsOffTerminal: off a terminal nothing is prompted for, and the
// error names where the credentials go.
func TestGetCredsOffTerminal(t *testing.T) {
	interactive := stdinIsTerminal

	stdinIsTerminal = func() bool { return false }

	t.Cleanup(func() { stdinIsTerminal = interactive })

	t.Setenv(EnvSdkUsername, "")
	t.Setenv(EnvSdkPassword, "")

	_, err := GetCreds("")
	require.ErrorIs(t, err, ErrNoCredentials)
	require.ErrorContains(t, err, "--credentials-file FILE")
	require.ErrorContains(t, err, "TT_CLI_EE_USERNAME and TT_CLI_EE_PASSWORD")

	t.Setenv(EnvSdkUsername, "user")

	_, err = GetCreds("")
	require.ErrorIs(t, err, errPasswordNotSet)
	require.EqualError(t, err,
		"password not set: TT_CLI_EE_USERNAME is set, TT_CLI_EE_PASSWORD is not")

	t.Setenv(EnvSdkUsername, "")
	t.Setenv(EnvSdkPassword, "secret")

	_, err = GetCreds("")
	require.ErrorIs(t, err, errLoginNotSet)

	t.Setenv(EnvSdkUsername, "user")

	creds, err := GetCreds("")
	require.NoError(t, err)
	assert.Equal(t, UserCredentials{Username: "user", Password: "secret"}, creds)
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
			wantErr: func(t assert.TestingT, err error, i ...any) bool {
				return err.Error() == "no credentials in environment variables were found"
			},
		},
		{
			name: "Environment variables are passed",
			prepare: func() {
				t.Setenv(EnvSdkUsername, "tt_test")
				t.Setenv(EnvSdkPassword, "tt_test")
			},
			want: UserCredentials{Username: "tt_test", Password: "tt_test"},
			wantErr: func(t assert.TestingT, err error, i ...any) bool {
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
			if !tt.wantErr(t, err, "getCredsFromEnvVars()") {
				return
			}

			assert.Equalf(t, tt.want, got, "getCredsFromEnvVars()")
		})
	}
}
