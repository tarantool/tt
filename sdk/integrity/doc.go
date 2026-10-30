// Package integrity defines the integrity-checking interface of tt: a
// repository for reading verified files, signing, and the hashers and
// verifiers used by storage-backed readers and writers.
//
// A Provider implements the interface. The disabled provider, returned by
// NewDisabledProvider, performs no checks, and the package-level functions
// delegate to it. tt runs with the provider its distribution chooses
// (core.WithIntegrity in github.com/tarantool/tt/v3/core) and with the
// disabled one when it chooses none; a module does not choose.
//
// The RSA provider, returned by NewRSAProvider, signs with an RSA private
// key and checks with its public key. Its Signer writes hashes files that
// list the SHA-256 digests of the files of a tree, each with an RSASSA-PSS
// signature next to it: for the tree of a package, hashes.json lists what
// Tarantool loads and env_hashes.json everything else, so that every file
// is listed once; for a runtime a package bundles, env_hashes.json lists the
// files under the paths they are installed at. Its check verifies the
// signatures and the digests of the hashes files of every root it is given
// - an application and the runtimes it runs on - and its Repository hashes
// a file again every time it reads it. VerifyTree checks that a whole tree
// is signed, before it is installed. Data published to a configuration
// storage carries a digest and a signature of the same kind.
//
// A signature of RSASSA-PSS has a random salt: signing the same tree twice
// gives the same hashes files and different signatures.
//
// The API of this package is alpha and may change incompatibly.
package integrity
