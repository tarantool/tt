package console

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/user"
)

// tailChunkSize is how many bytes getLastNLinesBegin reads at a time while
// it walks a file backwards.
const tailChunkSize int64 = 10000

// getHomeDir returns the home directory of the current user.
func getHomeDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("cannot get the current user: %w", err)
	}

	return usr.HomeDir, nil
}

// isRegularFile reports whether filePath names an existing regular file.
func isRegularFile(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.Mode().IsRegular()
}

// readFromPos reads into buf from the position pos of readSeeker.
func readFromPos(readSeeker io.ReadSeeker, pos int64, buf []byte) (int, error) {
	_, err := readSeeker.Seek(pos, io.SeekStart)
	if err != nil {
		return 0, fmt.Errorf("failed to seek: %w", err)
	}

	n, err := readSeeker.Read(buf)
	if err != nil {
		return n, fmt.Errorf("failed to read: %w", err)
	}

	return n, nil
}

// getLastNLinesBegin returns the offset at which the given number of last
// lines of the file begin. A negative number counts as its absolute value,
// and zero means the whole file.
func getLastNLinesBegin(filepath string, lines int) (int64, error) {
	if lines == 0 {
		return 0, nil
	}

	if lines < 0 {
		lines = -lines
	}

	file, err := os.Open(filepath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	fileInfo, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to get fileinfo: %w", err)
	}

	fileSize := fileInfo.Size()
	if fileSize == 0 {
		return 0, nil
	}

	buf := make([]byte, tailChunkSize)

	// A last line without a trailing newline counts as a line too.
	_, err = readFromPos(file, fileSize-1, buf)
	if err != nil {
		return 0, err
	}

	newLinesN := 0
	if buf[0] != '\n' {
		newLinesN++
	}

	filePos := fileSize - tailChunkSize
	lastPart := false

	for {
		if filePos < 0 {
			filePos = 0
			lastPart = true
		}

		n, err := readFromPos(file, filePos, buf)
		if err != nil {
			return 0, err
		}

		for pos := n - 1; pos >= 0; pos-- {
			if buf[pos] == '\n' {
				newLinesN++
			}

			if newLinesN == lines+1 {
				return filePos + int64(pos+1), nil
			}
		}

		if lastPart || filePos == 0 {
			return 0, nil
		}

		filePos -= tailChunkSize
	}
}

// getLastNLines returns the last linesN lines of the file, without their
// line terminators.
func getLastNLines(filepath string, linesN int) ([]string, error) {
	begin, err := getLastNLinesBegin(filepath, linesN)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	_, err = file.Seek(begin, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek in file: %w", err)
	}

	lines := []string{}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, nil
}
