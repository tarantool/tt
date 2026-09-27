package integrity

import (
	"crypto/rsa"

	"github.com/spf13/pflag"
	gcrypto "github.com/tarantool/go-storage/v2/crypto"
	ghasher "github.com/tarantool/go-storage/v2/hasher"
)

// IntegrityCtx is context required for integrity checks.
type IntegrityCtx struct {
	// Repository is a repository used to check integrity of files.
	Repository Repository

	// publicKey verifies signatures. The RSA provider sets it when a
	// public key enables the checks; it is nil otherwise.
	publicKey *rsa.PublicKey
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
	return disabledProvider{}.NewSigner(path)
}

// RegisterWithIntegrityFlag is a noop function that is intended to add
// integrity flags to commands that publish signed data.
func RegisterWithIntegrityFlag(flagset *pflag.FlagSet, dst *string) {
	disabledProvider{}.RegisterWithIntegrityFlag(flagset, dst)
}

// RegisterIntegrityCheckFlag is a noop function that is intended to add
// root flag enabling integrity checks.
func RegisterIntegrityCheckFlag(flagset *pflag.FlagSet, dst *string) {
	disabledProvider{}.RegisterIntegrityCheckFlag(flagset, dst)
}

// RegisterIntegrityCheckPeriodFlag is a noop function that is intended to
// add flag specifying how often should integrity checks run in watchdog.
func RegisterIntegrityCheckPeriodFlag(flagset *pflag.FlagSet, dst *int) {
	disabledProvider{}.RegisterIntegrityCheckPeriodFlag(flagset, dst)
}

// InitializeIntegrityCheck is a noop setup of integrity checking.
func InitializeIntegrityCheck(
	publicKeyPath, configDir string,
) (IntegrityCtx, error) {
	return disabledProvider{}.InitializeIntegrityCheck(publicKeyPath, configDir)
}

// GetCheckFunction returns a function that checks a map of hashes and a
// signature of a data.
func GetCheckFunction(ctx IntegrityCtx) (
	func(data []byte, hashes map[string][]byte, sign []byte) error, error,
) {
	return disabledProvider{}.GetCheckFunction(ctx)
}

// GetStorageVerifiers returns integrity primitives for storage-backed readers.
func GetStorageVerifiers(ctx IntegrityCtx) ([]ghasher.Hasher, []gcrypto.Verifier, error) {
	return disabledProvider{}.GetStorageVerifiers(ctx)
}

// GetSignFunction returns a function that creates a map of hashes and a
// signature for a data for the private key in the path.
func GetSignFunction(privateKeyPath string) (
	func(data []byte) (map[string][]byte, []byte, error), error,
) {
	return disabledProvider{}.GetSignFunction(privateKeyPath)
}

// GetStorageSigners returns integrity primitives for storage-backed writers.
func GetStorageSigners(
	privateKeyPath string,
) ([]ghasher.Hasher, []gcrypto.SignerVerifier, error) {
	return disabledProvider{}.GetStorageSigners(privateKeyPath)
}
