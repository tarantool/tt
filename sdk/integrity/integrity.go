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

// Signer writes signed hashes files: the lists of the files of a tree with
// their digests that a check rooted at the tree knows them by.
type Signer interface {
	// Sign signs the tree of a package rooted at dir. It writes two hashes
	// files in dir, each with its signature: hashes.json lists the files
	// Tarantool loads - Lua sources, shared libraries, cluster configs, the
	// files opts.Loaded names - and is what tt hands Tarantool to check
	// them; env_hashes.json lists every other file of the tree, the ones tt
	// reads and runs itself. Every file of the tree is listed exactly once,
	// but for the directories opts.Skip names and the hashes files and
	// signatures themselves, wherever they are.
	Sign(dir string, opts SignOptions) error
	// SignFiles writes the environment hashes file of dir, env_hashes.json,
	// listing files under the paths they are to have there, and its
	// signature. It signs a tree that is laid out after it is signed, such
	// as the runtime a package bundles, which is installed apart from it.
	SignFiles(dir string, files []SignedFile) error
}

// SignOptions tune what Signer.Sign lists.
type SignOptions struct {
	// Skip are directories of the tree, as slash-separated paths relative
	// to its root, that are not listed: what is installed apart from the
	// package, such as the runtime it bundles.
	Skip []string
	// Loaded are files of the tree, as slash-separated paths relative to its
	// root, that Tarantool loads whatever their names, such as the scripts
	// the instance records run. Each must be a file of the tree.
	Loaded []string
}

// SignedFile is a file Signer.SignFiles lists.
type SignedFile struct {
	// Path is the path the file is listed under: relative to the directory
	// of the hashes file, slash-separated, without "." or "..".
	Path string
	// Source is the file whose content is listed.
	Source string
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

// InitializeIntegrityCheck is a noop setup of integrity checking of the
// files rooted at roots.
func InitializeIntegrityCheck(
	publicKeyPath string, roots ...string,
) (IntegrityCtx, error) {
	return disabledProvider{}.InitializeIntegrityCheck(publicKeyPath, roots...)
}

// VerifyTree refuses to check a tree: the disabled provider checks nothing.
func VerifyTree(publicKeyPath, dir string) error {
	return disabledProvider{}.VerifyTree(publicKeyPath, dir)
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
