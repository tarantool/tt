package build

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const (
	// dirPerm is the mode of a directory CopyFile creates.
	dirPerm os.FileMode = 0o750
	// filePerm is the mode of a copy of a file without permission bits.
	filePerm os.FileMode = 0o644
)

// CopyFile copies the file src to dst, created or truncated, with the
// permission bits of src, and creates the directory of dst first. A backend
// copies what its tool built into the directories of [Dirs] with it.
//
// The copy is written with no fork of tt in between: a process forked while
// a file is open for writing holds it until it calls exec, and Linux refuses
// to run a file open for writing (ETXTBSY, go.dev/issue/22315), so a program
// copied otherwise could fail to start while tt starts other processes.
func CopyFile(dst, src string) error {
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening the source: %w", err)
	}

	defer func() { _ = source.Close() }()

	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("reading the source mode: %w", err)
	}

	mode := info.Mode().Perm()
	if mode == 0 {
		mode = filePerm
	}

	err = os.MkdirAll(filepath.Dir(dst), dirPerm)
	if err != nil {
		return fmt.Errorf("creating the destination directory: %w", err)
	}

	err = writeNoFork(dst, source, mode)
	if err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}

	return nil
}

// writeNoFork creates path with mode, or truncates it, and writes to it what
// src holds, holding syscall.ForkLock for reading: os/exec forks under it.
func writeNoFork(path string, src io.Reader, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err //nolint:wrapcheck // The *PathError names the operation and the path.
	}

	_, err = io.Copy(file, src)

	return errors.Join(err, file.Close())
}
