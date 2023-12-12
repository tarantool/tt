package integrity_test

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/integrity"
)

func TestNewSigner(t *testing.T) {
	testCases := []struct {
		name           string
		privateKeyPath string
	}{
		{
			name:           "Empty path",
			privateKeyPath: "",
		},
		{
			name:           "Arbitrary path",
			privateKeyPath: "private.pem",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			signer, err := integrity.NewSigner(testCase.privateKeyPath)
			require.Nil(t, signer, "signer must not be created")
			require.EqualError(t, err, "integrity signer should never be created in ce",
				"an error should be produced")
		})
	}
}

func InitializeIntegrityCheck(t *testing.T) {
	testCases := []struct {
		name          string
		publicKeyPath string
		configDir     string
	}{
		{
			name:          "Empty key and config path",
			publicKeyPath: "",
			configDir:     "",
		},
		{
			name:          "Arbitrary key path, empty config path",
			publicKeyPath: "public.pem",
			configDir:     "",
		},
		{
			name:          "Empty key path, arbitrary config path",
			publicKeyPath: "",
			configDir:     "app",
		},
		{
			name:          "Arbitrary key and config path",
			publicKeyPath: "public.pem",
			configDir:     "app",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := integrity.InitializeIntegrityCheck(testCase.publicKeyPath, testCase.configDir)
			require.EqualError(t, err,
				"integrity checks should never be initialized in ce",
				"an error should be produced")
		})
	}
}

func TestRegisterWithIntegritySigner(t *testing.T) {
	someStr := ""

	testCases := []struct {
		name    string
		flagSet *pflag.FlagSet
		dst     *string
	}{
		{
			name:    "Empty flagSet and dst",
			flagSet: nil,
			dst:     nil,
		},
		{
			name:    "Empty dst",
			flagSet: &pflag.FlagSet{},
			dst:     nil,
		},
		{
			name:    "Empty flagSet",
			flagSet: nil,
			dst:     &someStr,
		},
		{
			name:    "Nothing empty",
			flagSet: &pflag.FlagSet{},
			dst:     nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			integrity.RegisterWithIntegrityFlag(testCase.flagSet, testCase.dst)

			if testCase.flagSet != nil {
				require.False(t, testCase.flagSet.HasFlags(),
					"command must not be modified")
			}
		})
	}
}

func TestRegisterIntegrityCheckFlag(t *testing.T) {
	someStr := ""

	testCases := []struct {
		name    string
		flagSet *pflag.FlagSet
		dst     *string
	}{
		{
			name:    "Empty flagSet and dst",
			flagSet: nil,
			dst:     nil,
		},
		{
			name:    "Empty dst",
			flagSet: &pflag.FlagSet{},
			dst:     nil,
		},
		{
			name:    "Empty flagSet",
			flagSet: nil,
			dst:     &someStr,
		},
		{
			name:    "Nothing empty",
			flagSet: &pflag.FlagSet{},
			dst:     nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			integrity.RegisterIntegrityCheckFlag(testCase.flagSet, testCase.dst)

			if testCase.flagSet != nil {
				require.False(t, testCase.flagSet.HasFlags(),
					"command must not be modified")
			}
		})
	}
}

func TestRegisterIntegrityCheckPeriodFlag(t *testing.T) {
	someInt := 0

	testCases := []struct {
		name    string
		flagSet *pflag.FlagSet
		dst     *int
	}{
		{
			name:    "Empty flagSet and dst",
			flagSet: nil,
			dst:     nil,
		},
		{
			name:    "Empty dst",
			flagSet: &pflag.FlagSet{},
			dst:     nil,
		},
		{
			name:    "Empty flagSet",
			flagSet: nil,
			dst:     &someInt,
		},
		{
			name:    "Nothing empty",
			flagSet: &pflag.FlagSet{},
			dst:     nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			integrity.RegisterIntegrityCheckPeriodFlag(testCase.flagSet, testCase.dst)

			if testCase.flagSet != nil {
				require.False(t, testCase.flagSet.HasFlags(),
					"command must not be modified")
			}
		})
	}
}

func TestNewCollectorFactory(t *testing.T) {
	factory, err := integrity.NewCollectorFactory()

	require.Nil(t, factory)
	require.Equal(t, err, integrity.ErrNotConfigured)
}

func TestNewPublisherFactory(t *testing.T) {
	factory, err := integrity.NewDataPublisherFactory("")

	require.Nil(t, factory)
	require.EqualError(t, err, "integrity publishers should never be created in ce")
}
