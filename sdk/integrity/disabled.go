package integrity

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
	gcrypto "github.com/tarantool/go-storage/v2/crypto"
	ghasher "github.com/tarantool/go-storage/v2/hasher"
)

// disabledProvider is a Provider that performs no integrity checks.
type disabledProvider struct{}

var _ Provider = disabledProvider{}

// dummyRepository implements Repository with no checks performed.
type dummyRepository struct{}

func (dummyRepository) Read(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}

	return f, nil
}

func (dummyRepository) ValidateAll() error { return nil }

var _ Repository = dummyRepository{}

// RegisterWithIntegrityFlag registers no flag.
func (disabledProvider) RegisterWithIntegrityFlag(*pflag.FlagSet, *string) {}

// RegisterIntegrityCheckFlag registers no flag.
func (disabledProvider) RegisterIntegrityCheckFlag(*pflag.FlagSet, *string) {}

// RegisterIntegrityCheckPeriodFlag registers no flag.
func (disabledProvider) RegisterIntegrityCheckPeriodFlag(*pflag.FlagSet, *int) {}

// HashesFileName is empty: there are no hashes files.
func (disabledProvider) HashesFileName() string { return HashesFileName }

// InitializeIntegrityCheck returns a context whose repository reads files
// without checks, and refuses a public key.
func (disabledProvider) InitializeIntegrityCheck(
	publicKeyPath, _ string,
) (IntegrityCtx, error) {
	if publicKeyPath != "" {
		return IntegrityCtx{}, ErrNoVerifierInCE
	}

	return IntegrityCtx{
		Repository: dummyRepository{},
	}, nil
}

// NewSigner refuses to create a signer.
func (disabledProvider) NewSigner(string) (Signer, error) {
	return nil, ErrNoSignerInCE
}

// GetCheckFunction reports that integrity checks are not configured.
func (disabledProvider) GetCheckFunction(IntegrityCtx) (
	func(data []byte, hashes map[string][]byte, sign []byte) error, error,
) {
	return nil, ErrNotConfigured
}

// GetSignFunction refuses to create a sign function.
func (disabledProvider) GetSignFunction(string) (
	func(data []byte) (map[string][]byte, []byte, error), error,
) {
	return nil, ErrNoSignerInCE
}

// GetStorageVerifiers reports that integrity checks are not configured.
func (disabledProvider) GetStorageVerifiers(
	IntegrityCtx,
) ([]ghasher.Hasher, []gcrypto.Verifier, error) {
	return nil, nil, ErrNotConfigured
}

// GetStorageSigners refuses to create signers.
func (disabledProvider) GetStorageSigners(
	string,
) ([]ghasher.Hasher, []gcrypto.SignerVerifier, error) {
	return nil, nil, ErrNoSignerInCE
}
