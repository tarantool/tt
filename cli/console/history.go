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

	"github.com/tarantool/tt/cli/util"
)

const (
	// DefaultHistoryFileName is used for the DefaultHistoryFile() helping method.
	DefaultHistoryFileName = ".tarantool_history"
	// DefaultHistoryLines is used for the DefaultHistoryFile() helping method.
	DefaultHistoryLines = 10000
)

// History implementation of active history handler.
type History struct {
	filepath    string
	maxCommands int
	commands    []string
	timestamps  []int64
}

// NewHistory create/open specified file.
func NewHistory(file string, maxCommands int) (History, error) {
	h := History{
		filepath:    file,
		maxCommands: maxCommands,
		commands:    make([]string, 0),
		timestamps:  make([]int64, 0),
	}
	err := h.load()
	return h, err
}

// DefaultHistoryFile create/open history file with default parameters.
func DefaultHistoryFile() (History, error) {
	dir, err := util.GetHomeDir()
	if err != nil {
		return History{}, fmt.Errorf("failed to get home directory: %w", err)
	}
	file := filepath.Join(dir, DefaultHistoryFileName)
	return NewHistory(file, DefaultHistoryLines)
}

func (h *History) load() error {
	if !util.IsRegularFile(h.filepath) {
		return nil
	}
	rawLines, err := util.GetLastNLines(h.filepath, h.maxCommands)
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
		j := startPos + 1

		// Move pointer to the next timestamp.
		for j < len(lines) && !timeRecord.MatchString(lines[j]) {
			j++
		}

		// Extract the current timestamp.
		timestamp, err := strconv.ParseInt(lines[startPos][1:], 10, 0)

		if j != startPos+1 && err == nil {
			h.timestamps = append(h.timestamps, timestamp)
			h.commands = append(h.commands, strings.Join(lines[startPos+1:j], "\n"))
		}
		startPos = j
	}
}

// writeToFile writes console history to the file.
func (h *History) writeToFile() error {
	buff := bytes.Buffer{}
	for i, c := range h.commands {
		buff.WriteString(fmt.Sprintf("#%d\n%s\n", h.timestamps[i], c))
	}
	if err := os.WriteFile(h.filepath, buff.Bytes(), 0640); err != nil {
		return fmt.Errorf("failed to write to history file: %s", err)
	}

	return nil
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
	h.writeToFile()
}

// Commands implements HistoryKeeper.Commands interface method.
func (h *History) Commands() []string {
	return h.commands
}

// Close implements HistoryKeeper.Close interface method.
func (h *History) Close() {
}
