package integrity

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// openRegularFile opens the file at path for reading, following symbolic
// links, and refuses it unless it is a regular file.
//
// The file is opened without blocking and its type is checked on the open
// descriptor. Opening a FIFO without a writer would otherwise wait for
// one, and checking the path before opening it would leave a moment for
// a FIFO to be put in its place.
func openRegularFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|openNonBlock, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to check file: %w", err), file.Close())
	}

	if !info.Mode().IsRegular() {
		return nil, errors.Join(
			fmt.Errorf("%q is %w: %s", path, errNotRegularFile, fileTypeName(info.Mode())),
			file.Close())
	}

	return file, nil
}

// readRegularFile returns the content of the regular file at path,
// following symbolic links. The file is opened as openRegularFile does.
func readRegularFile(path string) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(file)
	closeErr := file.Close()

	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", path, err)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("failed to close %q: %w", path, closeErr)
	}

	return data, nil
}

// fileTypeName names the type of a file that is not a regular one.
func fileTypeName(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeCharDevice != 0:
		return "a character device"
	case mode&fs.ModeDevice != 0:
		return "a device"
	default:
		return "an irregular file (" + mode.Type().String() + ")"
	}
}
