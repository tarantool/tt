package integrity

import (
	"errors"

	"github.com/spf13/pflag"
)

var FileRepository Repository = dummyRepository{}

var HashesName = ""

// Signer implements high-level API for package signing.
type Signer interface {
	// Sign generates data to sign a package.
	Sign(basePath string, appNames []string) error
}

// NewSigner constructs a noop Signer.
func NewSigner(path string) (Signer, error) {
	return nil, errors.New("integrity signer should never be created in ce")
}

// RegisterWithIntegrityFlag is a noop function that is intended to add
// flags to `tt pack` command.
func RegisterWithIntegrityFlag(flagset *pflag.FlagSet, dst *string) {}

// RegisterIntegrityCheckFlag is a noop function that is intended to add
// root flag enabling integrity checks.
func RegisterIntegrityCheckFlag(flagset *pflag.FlagSet, dst *string) {}

// RegisterIntegrityCheckPeriodFlag is a noop function that is intended to
// add flag specifying how often should integrity checks run in watchdog.
func RegisterIntegrityCheckPeriodFlag(flagset *pflag.FlagSet, dst *int) {}

// InitializeIntegrityCheck is a noop setup of integrity checking.
func InitializeIntegrityCheck(publicKeyPath string, configDir string) error {
	return errors.New("integrity checks should never be initialized in ce")
}
