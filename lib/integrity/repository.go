package integrity

import "io"

// Repository provides utilities for working with files and
// ensuring that they were not compomised.
type Repository interface {
	// Read makes sure the file is not modified and reads it.
	Read(path string) (io.ReadCloser, error)
	// ValidateAll checks that all the files stored in the repository
	// were not modified.
	ValidateAll() error
}
