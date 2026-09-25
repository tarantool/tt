package console

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultHistoryFileName is used for the DefaultHistoryFile() helping method.
	DefaultHistoryFileName = ".tarantool_history"
	// DefaultHistoryLines is used for the DefaultHistoryFile() helping method.
	DefaultHistoryLines = 10000
	historyFileMode     = 0o640
)

// History is a [HistoryKeeper] that keeps the commands in a file.
//
// The file holds each command as a line "#<unix time>" followed by the
// command's lines, the format tt connect keeps its history in too. A file
// with no such time lines is read as one command per line. Every appended
// command rewrites the whole file.
type History struct {
	filepath    string
	maxCommands int
	commands    []string
	timestamps  []int64
}

// NewHistory opens the history kept in file. It reads the last maxCommands
// lines of the file, if it exists, and keeps at most maxCommands commands
// from then on; the file is created on the first appended command.
func NewHistory(file string, maxCommands int) (History, error) {
	history := History{
		filepath:    file,
		maxCommands: maxCommands,
		commands:    make([]string, 0),
		timestamps:  make([]int64, 0),
	}
	err := history.load()

	return history, err
}

// DefaultHistoryFile create/open history file with default parameters.
func DefaultHistoryFile() (History, error) {
	dir, err := getHomeDir()
	if err != nil {
		return History{}, fmt.Errorf("failed to get home directory: %w", err)
	}

	file := filepath.Join(dir, DefaultHistoryFileName)

	return NewHistory(file, DefaultHistoryLines)
}

// AppendCommand insert new command to the history file.
// Implements HistoryKeeper.AppendCommand interface method.
func (h *History) AppendCommand(input string) {
	h.commands = append(h.commands, input)
	h.timestamps = append(h.timestamps, time.Now().Unix())

	if len(h.commands) > h.maxCommands {
		h.commands = h.commands[1:]
		h.timestamps = h.timestamps[1:]
	}

	_ = h.writeToFile()
}

// Commands implements HistoryKeeper.Commands interface method.
func (h *History) Commands() []string {
	return h.commands
}

// Close implements HistoryKeeper.Close interface method.
func (h *History) Close() {
}

func (h *History) load() error {
	if !isRegularFile(h.filepath) {
		return nil
	}

	rawLines, err := getLastNLines(h.filepath, h.maxCommands)
	if err != nil {
		return err
	}

	h.parseCells(rawLines)

	return nil
}

func (h *History) parseCells(lines []string) {
	timeRecord := regexp.MustCompile(`^#\d+$`)

	// startPos is the first position of a timestamp.
	startPos := -1

	for i, line := range lines {
		if timeRecord.MatchString(line) {
			startPos = i
			break
		}
	}

	if startPos == -1 {
		// Read one line per command.
		// Set the current timestamp for each command.
		h.commands = lines

		now := time.Now().Unix()

		for range lines {
			h.timestamps = append(h.timestamps, now)
		}

		return
	}

	for startPos < len(lines) {
		nextPos := startPos + 1

		// Move pointer to the next timestamp.
		for nextPos < len(lines) && !timeRecord.MatchString(lines[nextPos]) {
			nextPos++
		}

		// Extract the current timestamp.
		timestamp, err := strconv.ParseInt(lines[startPos][1:], 10, 0)

		if nextPos != startPos+1 && err == nil {
			h.timestamps = append(h.timestamps, timestamp)
			h.commands = append(h.commands, strings.Join(lines[startPos+1:nextPos], "\n"))
		}

		startPos = nextPos
	}
}

// writeToFile writes console history to the file.
func (h *History) writeToFile() error {
	buff := bytes.Buffer{}
	for i, c := range h.commands {
		fmt.Fprintf(&buff, "#%d\n%s\n", h.timestamps[i], c)
	}

	err := os.WriteFile(h.filepath, buff.Bytes(), historyFileMode)
	if err != nil {
		return fmt.Errorf("failed to write to history file: %w", err)
	}

	return nil
}
