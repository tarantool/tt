package cluster

import (
	"io"
)

// SignFunc creates a map of hashes and a signature for a data.
type SignFunc func(data []byte) (map[string][]byte, []byte, error)

// CheckFunc checks a map of hashes and a signature of a data.
type CheckFunc func(data []byte, hashes map[string][]byte, sign []byte) error

// FileReadFunc reads a file.
type FileReadFunc func(path string) (io.ReadCloser, error)
