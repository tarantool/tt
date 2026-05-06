package integrity

import (
	"github.com/spf13/pflag"
)

// IntegrityCtx is context required for integrity checks.
type IntegrityCtx struct {
	// Repository is a repository used to check integrity of files.
	Repository Repository
}

// HashesFileName is a name of a file containing file hashes that
// require checking.
const HashesFileName = ""

// Signer implements high-level API for package signing.
type Signer interface {
	// Sign generates data to sign a package.
	Sign(basePath string, appNames []string) error
}

// NewSigner constructs a noop Signer.
func NewSigner(path string) (Signer, error) {
	return nil, ErrNoSignerInCE
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
func InitializeIntegrityCheck(
	publicKeyPath, configDir, instancesEnabledDir string,
) (IntegrityCtx, error) {
	if publicKeyPath != "" {
		return IntegrityCtx{}, ErrNoVerifierInCE
	}

	return IntegrityCtx{
		Repository: dummyRepository{},
	}, nil
}

// GetCheckFunction returns a function that checks a map of hashes and a
// signature of a data.
func GetCheckFunction(ctx IntegrityCtx) (
	func(data []byte, hashes map[string][]byte, sign []byte) error, error,
) {
	return nil, ErrNotConfigured
}

// GetSignFunction returns a function that creates a map of hashes and a
// signature for a data for the private key in the path.
func GetSignFunction(privateKeyPath string) (
	func(data []byte) (map[string][]byte, []byte, error), error,
) {
	return nil, ErrNoSignerInCE
}
