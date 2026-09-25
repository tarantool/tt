package integrity

import (
	"github.com/spf13/pflag"
	gcrypto "github.com/tarantool/go-storage/v2/crypto"
	ghasher "github.com/tarantool/go-storage/v2/hasher"
)

// Provider is an implementation of integrity checking. Each method matches
// the package-level function of the same name; the package-level functions
// are the disabled provider.
//
// A caller selects the provider once and passes it to everything that
// registers integrity flags, initializes integrity checking or builds
// signers and verifiers. An IntegrityCtx is only meaningful to the provider
// that created it.
type Provider interface {
	// RegisterWithIntegrityFlag adds the flag naming the private key used
	// to sign published data.
	RegisterWithIntegrityFlag(flags *pflag.FlagSet, dst *string)
	// RegisterIntegrityCheckFlag adds the root flag naming the public key
	// that enables integrity checks.
	RegisterIntegrityCheckFlag(flags *pflag.FlagSet, dst *string)
	// RegisterIntegrityCheckPeriodFlag adds the flag setting how often the
	// watchdog re-checks integrity.
	RegisterIntegrityCheckPeriodFlag(flags *pflag.FlagSet, dst *int)
	// HashesFileName is the name of the file with the hashes of an
	// application's files.
	HashesFileName() string
	// InitializeIntegrityCheck sets up integrity checking of the
	// environment rooted at configDir with the public key at
	// publicKeyPath. An empty publicKeyPath disables the checks.
	InitializeIntegrityCheck(publicKeyPath, configDir string) (IntegrityCtx, error)
	// NewSigner creates a Signer of an environment for the private key at
	// privateKeyPath.
	NewSigner(privateKeyPath string) (Signer, error)
	// GetCheckFunction returns a function that checks a map of hashes and
	// a signature of a data.
	GetCheckFunction(ctx IntegrityCtx) (
		func(data []byte, hashes map[string][]byte, sign []byte) error, error,
	)
	// GetSignFunction returns a function that creates a map of hashes and a
	// signature for a data for the private key at privateKeyPath.
	GetSignFunction(privateKeyPath string) (
		func(data []byte) (map[string][]byte, []byte, error), error,
	)
	// GetStorageVerifiers returns integrity primitives for storage-backed
	// readers.
	GetStorageVerifiers(ctx IntegrityCtx) ([]ghasher.Hasher, []gcrypto.Verifier, error)
	// GetStorageSigners returns integrity primitives for storage-backed
	// writers for the private key at privateKeyPath.
	GetStorageSigners(
		privateKeyPath string,
	) ([]ghasher.Hasher, []gcrypto.SignerVerifier, error)
}

// NewDisabledProvider returns the provider that performs no integrity
// checks: it registers no flags, reads files as they are, and refuses to
// sign or to verify with a key.
func NewDisabledProvider() Provider {
	return disabledProvider{}
}
