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
// list the SHA-256 digests of the files of an environment, each with an
// RSASSA-PSS signature next to it. Its check verifies those signatures and
// digests, and its Repository hashes a file again every time it reads it.
// Data published to a configuration storage carries a digest and a
// signature of the same kind.
//
// The API of this package is alpha and may change incompatibly.
package integrity
