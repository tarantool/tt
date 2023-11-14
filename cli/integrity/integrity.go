package integrity

import (
	"errors"

	"github.com/spf13/pflag"
)

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
