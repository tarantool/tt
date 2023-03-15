package version

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type returnValueParseVersion struct {
	version Version
	err     error
}

func TestParseVersion(t *testing.T) {
	assert := assert.New(t)

	testCases := make(map[string]returnValueParseVersion)

	testCases["2.10.42-alpha2-91-g08c9b4963-r482"] = returnValueParseVersion{
		Version{
			Major:      2,
			Minor:      10,
			Patch:      42,
			Release:    Release{Type: TypeAlpha, Num: 2},
			Additional: 91,
			Hash:       "g08c9b4963",
			Revision:   482,
			Str:        "2.10.42-alpha2-91-g08c9b4963-r482",
		},
		nil,
	}

	testCases["1.10.13-48-ga3a42eec7-r496"] = returnValueParseVersion{
		Version{
			Major:      1,
			Minor:      10,
			Patch:      13,
			Release:    Release{Type: TypeRelease},
			Additional: 48,
			Hash:       "ga3a42eec7",
			Revision:   496,
			Str:        "1.10.13-48-ga3a42eec7-r496",
		},
		nil,
	}

	testCases["1.10.123-rc1-100-g2ba6c0"] = returnValueParseVersion{
		Version{
			Major:      1,
			Minor:      10,
			Patch:      123,
			Release:    Release{Type: TypeRC, Num: 1},
			Additional: 100,
			Hash:       "g2ba6c0",
			Str:        "1.10.123-rc1-100-g2ba6c0",
		},
		nil,
	}

	testCases["1.2.3-beta22-123"] = returnValueParseVersion{
		Version{
			Major:      1,
			Minor:      2,
			Patch:      3,
			Release:    Release{Type: TypeBeta, Num: 22},
			Additional: 123,
			Str:        "1.2.3-beta22-123",
		},
		nil,
	}

	testCases["1.2.3-rc12"] = returnValueParseVersion{
		Version{
			Major:   1,
			Minor:   2,
			Patch:   3,
			Release: Release{Type: TypeRC, Num: 12},
			Str:     "1.2.3-rc12",
		},
		nil,
	}

	testCases["3.2.1-entrypoint"] = returnValueParseVersion{
		Version{
			Major:   3,
			Minor:   2,
			Patch:   1,
			Release: Release{Type: TypeNightly},
			Str:     "3.2.1-entrypoint",
		},
		nil,
	}

	testCases["2.10.0"] = returnValueParseVersion{
		Version{
			Major:   2,
			Minor:   10,
			Patch:   0,
			Release: Release{Type: TypeRelease},
			Str:     "2.10.0",
		},
		nil,
	}

	testCases["v1.2.3"] = returnValueParseVersion{
		Version{
			Major:   1,
			Minor:   2,
			Patch:   3,
			Release: Release{Type: TypeRelease},
			Str:     "v1.2.3",
		},
		nil,
	}

	testCases["2.8"] = returnValueParseVersion{
		Version{},
		fmt.Errorf("failed to parse version \"2.8\": format is not valid"),
	}

	testCases["42"] = returnValueParseVersion{
		Version{},
		fmt.Errorf("failed to parse version \"42\": format is not valid"),
	}

	for input, output := range testCases {
		version, err := Parse(input)

		if output.err == nil {
			assert.Nil(err)
			assert.Equal(output.version, version)
		} else {
			assert.Equal(output.err, err)
		}
	}
}
