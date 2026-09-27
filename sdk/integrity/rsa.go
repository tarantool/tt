package integrity

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/spf13/pflag"
	gcrypto "github.com/tarantool/go-storage/v2/crypto"
	ghasher "github.com/tarantool/go-storage/v2/hasher"
)

const (
	withIntegrityFlagName        = "with-integrity-check"
	integrityCheckFlagName       = "integrity-check"
	integrityCheckPeriodFlagName = "integrity-check-period"
)

// errNoSHA256Hash is the error of data whose hashes have no SHA-256 digest.
var errNoSHA256Hash = errors.New(`hash "sha256" not found`)

// rsaProvider is a Provider that signs the files of an environment and the
// data it publishes with an RSA private key and checks them with the
// public key.
type rsaProvider struct{}

var _ Provider = rsaProvider{}

// NewRSAProvider returns the provider that signs with an RSA private key
// and checks with its public key. A signed environment carries hashes
// files that list the SHA-256 digests of its files, each signed with
// RSASSA-PSS; data published to a configuration storage carries its digest
// and signature.
func NewRSAProvider() Provider {
	return rsaProvider{}
}

// RegisterWithIntegrityFlag adds the flag naming the private key that
// signs published data and packages.
func (rsaProvider) RegisterWithIntegrityFlag(flags *pflag.FlagSet, dst *string) {
	flags.StringVar(dst, withIntegrityFlagName, *dst,
		"path to the private key that signs published data and packages")
}

// RegisterIntegrityCheckFlag adds the flag naming the public key that
// enables integrity checks.
func (rsaProvider) RegisterIntegrityCheckFlag(flags *pflag.FlagSet, dst *string) {
	flags.StringVar(dst, integrityCheckFlagName, *dst,
		"path to the public key that enables integrity checks")
}

// RegisterIntegrityCheckPeriodFlag adds the flag setting how often, in
// seconds, the watchdog re-checks integrity.
func (rsaProvider) RegisterIntegrityCheckPeriodFlag(flags *pflag.FlagSet, dst *int) {
	flags.IntVar(dst, integrityCheckPeriodFlagName, *dst,
		"how often the watchdog re-checks integrity, in seconds")
}

// HashesFileName is the name of the hashes file of an application.
func (rsaProvider) HashesFileName() string {
	return appHashesFileName
}

// InitializeIntegrityCheck checks the signed hashes files in configDir
// with the public key at publicKeyPath and every file they list. Without a
// public key the returned repository reads files unchecked.
func (rsaProvider) InitializeIntegrityCheck(
	publicKeyPath, configDir string,
) (IntegrityCtx, error) {
	if publicKeyPath == "" {
		return IntegrityCtx{Repository: dummyRepository{}, publicKey: nil}, nil
	}

	publicKey, err := loadPublicKey(publicKeyPath)
	if err != nil {
		return IntegrityCtx{}, err
	}

	repository, err := loadRepository(publicKey, configDir)
	if err != nil {
		return IntegrityCtx{}, err
	}

	return IntegrityCtx{Repository: repository, publicKey: publicKey}, nil
}

// NewSigner creates a Signer of an environment for the private key at
// privateKeyPath.
func (rsaProvider) NewSigner(privateKeyPath string) (Signer, error) {
	privateKey, err := loadPrivateKey(privateKeyPath)
	if err != nil {
		return nil, err
	}

	return rsaSigner{privateKey: privateKey}, nil
}

// GetCheckFunction returns a function that checks the SHA-256 digest and
// the signature of data, both hex text, with the public key of ctx.
func (rsaProvider) GetCheckFunction(ctx IntegrityCtx) (
	func(data []byte, hashes map[string][]byte, sign []byte) error, error,
) {
	publicKey := ctx.publicKey
	if publicKey == nil {
		return nil, ErrNotConfigured
	}

	return func(data []byte, hashes map[string][]byte, sign []byte) error {
		return checkData(publicKey, data, hashes, sign)
	}, nil
}

// GetSignFunction returns a function that computes the SHA-256 digest and
// the signature of data, both hex text, with the private key at
// privateKeyPath.
func (rsaProvider) GetSignFunction(privateKeyPath string) (
	func(data []byte) (map[string][]byte, []byte, error), error,
) {
	privateKey, err := loadPrivateKey(privateKeyPath)
	if err != nil {
		return nil, err
	}

	return func(data []byte) (map[string][]byte, []byte, error) {
		digest := sha256.Sum256(data)

		signature, err := signData(privateKey, data)
		if err != nil {
			return nil, nil, err
		}

		hashes := map[string][]byte{sha256Algorithm: hex.AppendEncode(nil, digest[:])}

		return hashes, hex.AppendEncode(nil, signature), nil
	}, nil
}

// GetStorageVerifiers returns the SHA-256 hasher and the RSA-PSS verifier
// of go-storage for the public key of ctx. Both accept a digest or a
// signature in raw or hex form.
func (rsaProvider) GetStorageVerifiers(
	ctx IntegrityCtx,
) ([]ghasher.Hasher, []gcrypto.Verifier, error) {
	if ctx.publicKey == nil {
		return nil, nil, ErrNotConfigured
	}

	return []ghasher.Hasher{ghasher.NewSHA256Hasher()},
		[]gcrypto.Verifier{gcrypto.NewRSAPSSVerifier(*ctx.publicKey)}, nil
}

// GetStorageSigners returns the SHA-256 hasher and the RSA-PSS signer of
// go-storage for the private key at privateKeyPath. Both produce hex text.
func (rsaProvider) GetStorageSigners(
	privateKeyPath string,
) ([]ghasher.Hasher, []gcrypto.SignerVerifier, error) {
	privateKey, err := loadPrivateKey(privateKeyPath)
	if err != nil {
		return nil, nil, err
	}

	return []ghasher.Hasher{ghasher.NewSHA256Hasher(ghasher.WithMode(ghasher.ModeHex))},
		[]gcrypto.SignerVerifier{
			gcrypto.NewRSAPSS(*privateKey, gcrypto.WithMode(gcrypto.ModeHex)),
		}, nil
}

// checkData checks the hex SHA-256 digest in hashes and the hex signature
// sign of data with publicKey. Hashes of other algorithms are ignored.
func checkData(publicKey *rsa.PublicKey, data []byte, hashes map[string][]byte, sign []byte) error {
	encodedDigest, ok := hashes[sha256Algorithm]
	if !ok {
		return errNoSHA256Hash
	}

	expected, err := hex.DecodeString(string(encodedDigest))
	if err != nil {
		return fmt.Errorf("hash %q is not hex: %w", sha256Algorithm, err)
	}

	actual := sha256.Sum256(data)
	if !bytes.Equal(expected, actual[:]) {
		return fmt.Errorf("%w: %q of the data is %q, expected %q", errHashMismatch,
			sha256Algorithm, hex.EncodeToString(actual[:]), hex.EncodeToString(expected))
	}

	signature, err := hex.DecodeString(string(sign))
	if err != nil {
		return fmt.Errorf("signature is not hex: %w", err)
	}

	err = verifyData(publicKey, data, signature)
	if err != nil {
		return fmt.Errorf("data does not match its signature: %w", err)
	}

	return nil
}
