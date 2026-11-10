// Package execfile writes files that a process may run - executables and
// scripts - so that they can be run while the process starts others.
//
// A process forked while a file is open for writing inherits the descriptor
// and holds it until it calls exec, and Linux refuses to run a file open for
// writing: running the file just written fails then with ETXTBSY, "text file
// busy" (go.dev/issue/22315). It takes no more than another goroutine
// starting a process at the wrong moment, as parallel tests do. os/exec forks
// under syscall.ForkLock, so a write that holds it for reading is one no
// fork of the process sees.
package execfile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"syscall"
)

// Write creates path with mode, or truncates it, and writes to it what src
// holds, with no fork of the process in between. The error is that of
// os.OpenFile, of io.Copy or of closing the file.
func Write(path string, src io.Reader, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err //nolint:wrapcheck // The *PathError names the operation and the path.
	}

	_, err = io.Copy(file, src)

	return errors.Join(err, file.Close())
}

// WriteFile writes data to path as os.WriteFile does, with no fork of the
// process in between.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	return Write(path, bytes.NewReader(data), mode)
}
