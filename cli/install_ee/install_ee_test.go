package install_ee

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/version"
)

type getVersionsInputValue struct {
	data *[]byte
}

type getVersionsOutputValue struct {
	result []version.Version
	err    error
}

func TestGetVersions(t *testing.T) {
	assert := assert.New(t)

	testCases := make(map[getVersionsInputValue]getVersionsOutputValue)

	inputData0 := []byte("random string")

	testCases[getVersionsInputValue{data: &inputData0}] =
		getVersionsOutputValue{
			result: nil,
			err:    fmt.Errorf("no packages for this OS"),
		}

	inputData1 := []byte(`<a href="/enterprise/tarantool-enterprise-bundle-` +
		`1.10.10-52-g0df29b137-r419.tar.gz">tarantool-enterprise-bundle-1.10.10-` +
		`52-g0df29b137-r419.tar.gz</a>                                      2021-08-18 ` +
		`15:56:04                   260136444`)

	testCases[getVersionsInputValue{data: &inputData1}] =
		getVersionsOutputValue{
			result: []version.Version{
				version.Version{
					Major:      1,
					Minor:      10,
					Patch:      10,
					Additional: 52,
					Revision:   419,
					Release:    version.Release{Type: version.TypeRelease},
					Hash:       "g0df29b137",
					Str:        "1.10.10-52-g0df29b137-r419",
					Tarball:    "tarantool-enterprise-bundle-1.10.10-52-g0df29b137-r419.tar.gz",
				},
			},
			err: nil,
		}

	for input, output := range testCases {
		versions, err := getVersions(input.data)

		if output.err == nil {
			assert.Nil(err)
			assert.Equal(output.result, versions)
		} else {
			assert.Equal(output.err, err)
		}
	}
}

type getCredsFromFileInputValue struct {
	path string
}

type getCredsFromFileOutputValue struct {
	result userCredentials
	err    error
}

func TestGetCredsFromFile(t *testing.T) {
	assert := assert.New(t)

	testCases := make(map[getCredsFromFileInputValue]getCredsFromFileOutputValue)

	testCases[getCredsFromFileInputValue{path: "./testdata/nonexisting"}] =
		getCredsFromFileOutputValue{
			result: userCredentials{},
			err:    fmt.Errorf("open ./testdata/nonexisting: no such file or directory"),
		}

	testCases[getCredsFromFileInputValue{path: "./testdata/creds_ok"}] =
		getCredsFromFileOutputValue{
			result: userCredentials{
				username: "toor",
				password: "1234",
			},
			err: nil,
		}

	testCases[getCredsFromFileInputValue{path: "./testdata/creds_bad"}] =
		getCredsFromFileOutputValue{
			result: userCredentials{},
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
