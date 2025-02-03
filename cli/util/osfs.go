package util

import (
	"io/fs"
	"os"
)

type osFS struct{}

// GetOsFS returns a default implementation of fs.FS interface. In general interface fs.FS
// should be added as an argument to any function where you need to be able to substitute
// non-default FS. The most obvious scenario is using mock FS for testing. In such a case
// while general code uses this default implementation, test code is able to substitute
// some mock FS (like fstest.MapFS).
func GetOsFS() fs.FS {
	return osFS{}
}

func (fs osFS) Open(name string) (fs.File, error) {
	return os.Open(name)
}

func (fs osFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}
