// Package integrity defines the integrity-checking interface of tt: a
// repository for reading verified files, signing, and the hashers and
// verifiers used by storage-backed readers and writers.
//
// A Provider implements the interface. The disabled provider, returned by
// NewDisabledProvider, performs no checks; it is what tt uses, and the
// package-level functions delegate to it.
//
// The API of this package is alpha and may change incompatibly.
package integrity
