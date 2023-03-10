package search

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

type getVersionsInputValue struct {
	data string
}

type getVersionsOutputValue struct {
	result BundleInfoSlice
	err    error
}

func TestGetVersions(t *testing.T) {
	assert := assert.New(t)

	testCases := make(map[getVersionsInputValue]getVersionsOutputValue)

	inputData0 := "random string"

	testCases[getVersionsInputValue{data: inputData0}] =
		getVersionsOutputValue{
			result: nil,
			err:    fmt.Errorf("no packages for this OS"),
		}

	arch, err := util.GetArch()
	assert.NoError(err)

	osType, err := util.GetOs()
	assert.NoError(err)
	osName := ""
	switch osType {
	case util.OsLinux:
		osName = ".linux."
	case util.OsMacos:
		osName = ".macos."
	}

	inputData1 := `/enterprise/tarantool-enterprise-sdk-gc64-1.10.10-52-r419` +
		osName +
		arch +
		`.tar.gz`

	testCases[getVersionsInputValue{data: inputData1}] =
		getVersionsOutputValue{
			result: []BundleInfo{
				BundleInfo{Version: version.Version{
					Major:      1,
					Minor:      10,
					Patch:      10,
					Additional: 52,
					Revision:   419,
					Release:    version.Release{Type: version.TypeRelease},
					Hash:       "",
					Str:        "gc64-1.10.10-52-r419",
					Tarball: "tarantool-enterprise-sdk-gc64-1.10.10-52-r419" +
						osName + arch + ".tar.gz",
					GCSuffix: "gc64",
				}, Prefix: "/enterprise/"},
			},
			err: nil,
		}

	osNameOld := ""
	switch osType {
	case util.OsLinux:
		osNameOld = "-linux-"
	case util.OsMacos:
		osNameOld = "-macosx-"
	}
	inputData2 := `/enterprise/tarantool-enterprise-bundle-5.5.5-0-g32e3bd111-r100` +
		osNameOld +
		arch +
		`.tar.gz`

	testCases[getVersionsInputValue{data: inputData2}] =
		getVersionsOutputValue{
			result: []BundleInfo{
				BundleInfo{Version: version.Version{
					Major:      5,
					Minor:      5,
					Patch:      5,
					Additional: 0,
					Revision:   100,
					Release:    version.Release{Type: version.TypeRelease},
					Hash:       "g32e3bd111",
					Str:        "5.5.5-0-g32e3bd111-r100",
					Tarball: "tarantool-enterprise-bundle-5.5.5-0-g32e3bd111-r100" +
						osNameOld + arch + ".tar.gz",
					GCSuffix: "",
				}, Prefix: "/enterprise/"},
			},
			err: nil,
		}

	for input, output := range testCases {
		versions, err := getBundles([]string{input.data})

		if output.err == nil {
			assert.Nil(err)
			assert.Equal(output.result, versions)
		} else {
			assert.Equal(output.err, err)
		}
	}
}
