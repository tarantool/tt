package integrity

import (
	"errors"

	"github.com/spf13/pflag"
)

var (
	// ErrNotConfigured is reported when integrity check is not configured
	// in the command context.
	ErrNotConfigured = errors.New("integrity check is not configured")
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
func InitializeIntegrityCheck(publicKeyPath string, configDir string) (IntegrityCtx, error) {
	if publicKeyPath != "" {
		return IntegrityCtx{}, errors.New("integrity checks should never be initialized in ce")
	}

	return IntegrityCtx{
		Repository: dummyRepository{},
	}, nil
}

// NewDataCollectorFactory creates a new CollectorFactory with integrity checks
// in collectors. In the CE implementation it always returns ErrNotConfigured.
func NewDataCollectorFactory(ctx IntegrityCtx) (DataCollectorFactory, error) {
	return nil, ErrNotConfigured
}

// NewDataPublisherFactory create a new DataPublisherFactory with integrity
// algorithms in publishers. Should be never be called in the CE.
func NewDataPublisherFactory(path string) (DataPublisherFactory, error) {
	return nil, errors.New("integrity publishers should never be created in ce")
}
