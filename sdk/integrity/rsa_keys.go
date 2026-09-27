package integrity

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// privateKeyMode is the widest set of permission bits a private key file
// may have: read and write for its owner only.
const privateKeyMode fs.FileMode = 0o600

var (
	errKeyFileTooOpen = errors.New("only its owner may read or write it")
	errNoPEMBlock     = errors.New("no pem block found")
	errNotRSAKey      = errors.New("not an RSA key")
)

// loadPrivateKey reads the PKCS #1 RSA private key from the PEM file at
// path. The file is refused when its permissions give access to anyone but
// its owner.
func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to check private key: %w", err)
	}

	perm := info.Mode().Perm()
	if perm&^privateKeyMode != 0 {
		return nil, fmt.Errorf("permissions for private key file %q are too open (%#o): %w",
			path, perm, errKeyFileTooOpen)
	}

	block, err := readPEMBlock(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key %q: %w", path, err)
	}

	return privateKey, nil
}

// loadPublicKey reads the RSA public key, PKIX-encoded, from the PEM file
// at path.
func loadPublicKey(path string) (*rsa.PublicKey, error) {
	block, err := readPEMBlock(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load public key: %w", err)
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key %q: %w", path, err)
	}

	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key %q: %w: %T", path, errNotRSAKey, parsed)
	}

	return publicKey, nil
}

// readPEMBlock returns the first PEM block of the file at path. The block's
// type is not checked: the key it holds is whatever its DER parses as.
func readPEMBlock(path string) (*pem.Block, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%q: %w", path, errNoPEMBlock)
	}

	return block, nil
}

// pssOptions are the RSASSA-PSS parameters of every signature: SHA-256
// with a salt as long as its digest.
func pssOptions() *rsa.PSSOptions {
	return &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA256,
	}
}

// signData returns the RSASSA-PSS signature of the SHA-256 digest of data.
func signData(privateKey *rsa.PrivateKey, data []byte) ([]byte, error) {
	digest := sha256.Sum256(data)

	signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, digest[:], pssOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	return signature, nil
}

// verifyData checks that signature is the RSASSA-PSS signature of the
// SHA-256 digest of data. The error of a signature that does not verify
// wraps the one of crypto/rsa.
func verifyData(publicKey *rsa.PublicKey, data, signature []byte) error {
	digest := sha256.Sum256(data)

	err := rsa.VerifyPSS(publicKey, crypto.SHA256, digest[:], signature, pssOptions())
	if err != nil {
		return fmt.Errorf("failed to verify signature: %w", err)
	}

	return nil
}
