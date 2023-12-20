package integrity

import (
	"io"
	"os"
)

// dummyRepository implements repository with no checks performed.
type dummyRepository struct{}

// AddHash is a stub of method used to add hashing algorithm in the
// proper implementation.
func (dummyRepository) AddHash(hasher any) {}

// Add is a stub of method used to add file with hashes to a repository.
func (dummyRepository) Add(path string) error { return nil }

// Read opens the supplied file.
func (dummyRepository) Read(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// ValidateAll doesn't do anything since dummyRepository does not implement
// validation.
func (dummyRepository) ValidateAll() error { return nil }

// NewDummyRepository constructs a dummy repository
func NewDummyRepository() Repository {
	return dummyRepository{}
}

var _ Repository = dummyRepository{}
