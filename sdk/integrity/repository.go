package integrity

import "io"

// Repository provides utilities for working with files and
// ensuring that they were not compromised.
//
// The guarantees below hold while integrity checking is on. The
// repository of the disabled provider, and of a check initialized
// without a public key, checks nothing: Read and ReadFile return files
// as they are and ValidateAll reports nothing.
type Repository interface {
	// Read checks the file at path and returns it opened for reading.
	//
	// The content of the file matched its digest when Read opened it. The
	// returned file is the live file: a write to it after Read returns
	// reaches the reader undetected, and only a later ValidateAll reports
	// it. Meant for large files and for callers that need a file rather
	// than bytes.
	Read(path string) (io.ReadCloser, error)
	// ReadFile checks the file at path and returns its content.
	//
	// The file is read into memory once and those bytes are checked, so
	// the returned bytes are exactly the ones that matched the digest,
	// whatever happens to the file afterwards. The cost is memory
	// proportional to the file. Meant for configuration and script files.
	ReadFile(path string) ([]byte, error)
	// ValidateAll checks that all the files stored in the repository
	// were not modified.
	ValidateAll() error
}
