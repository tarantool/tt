package pack

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateDebianBinary(t *testing.T) {
	testCases := []struct {
		name       string
		packageDir string
		correctErr func(err error) bool
	}{
		{
			name:       "Correct directory",
			packageDir: t.TempDir(),
			correctErr: func(err error) bool {
				return err == nil
			},
		},
		{
			name:       "Non-existing directory",
			packageDir: "nothing",
			correctErr: func(err error) bool {
				return strings.Contains(err.Error(),
					"no such file or directory")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := createDebianBinary(testCase.packageDir)
			require.Equal(t, true, testCase.correctErr(err), "wrong error caught")
		})
	}
}
