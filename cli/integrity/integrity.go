package integrity

import (
	"errors"

	"github.com/spf13/pflag"

	"github.com/tarantool/tt/cli/cluster"
)

var (
	ErrNotConfigured = errors.New("integration check is not configured")
)

type IntegrityCtx struct {
	Repository Repository
}

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
func InitializeIntegrityCheck(publicKeyPath string, configDir string, ctx *IntegrityCtx) error {
	if publicKeyPath != "" {
		return errors.New("integrity checks should never be initialized in ce")
	}

	ctx.Repository = dummyRepository{}
	return nil
}

// NewCollectorFactory creates a new CollectorFactory with integrity checks
// in collectors. In the CE implementation it always returns ErrNotConfigured.
func NewCollectorFactory(ctx IntegrityCtx) (cluster.CollectorFactory, error) {
	return nil, ErrNotConfigured
}

// NewDataPublisherFactory create a new DataPublisherFactory with integrity
// algorithms in publishers. Should be never be called in the CE.
func NewDataPublisherFactory(path string) (cluster.DataPublisherFactory, error) {
	return nil, errors.New("integrity publishers should never be created in ce")
}
