package connect

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

// commandHistory stores console command history.
type commandHistory struct {
	filepath    string
	commands    []string
	timestamps  []int64
	maxCommands int
}

// parseHistoryCells extracts timestamps and commands from history lines.
// If format doesn't contain timestamps, it sets the current timestamp
// for each command.
func parseHistoryCells(
	lines []string,
) (commands []string, timestamps []int64) {
	timestampRegex := regexp.MustCompile(`^#\d+$`)

	// startPos is the first position of a timestamp.
	startPos := -1
	for i, line := range lines {
		if timestampRegex.MatchString(line) {
			startPos = i
			break
		}
	}
	timestamps = make([]int64, 0)
	if startPos == -1 {
		// Read one line per command.
		// Set the current timestamp for each command.
		commands = lines
		timestamp := time.Now().Unix()
		for range lines {
			timestamps = append(timestamps, timestamp)
		}
		return
	}

	commands = make([]string, 0)
	for startPos < len(lines) {
		j := startPos + 1

		// Move pointer to the next timestamp.
		for j < len(lines) && !timestampRegex.MatchString(lines[j]) {
			j++
		}

		// Extract the current timestamp.
		timestamp, err := strconv.ParseInt(lines[startPos][1:], 10, 0)

		if j != startPos+1 && err == nil {
			timestamps = append(timestamps, timestamp)
			commands = append(commands, strings.Join(lines[startPos+1:j], "\n"))
		}
		startPos = j
	}

	return
}

// load loads console history from the history file.
func (history *commandHistory) load() error {
	rawLines, err := util.GetLastNLines(history.filepath, history.maxCommands)
	if err != nil {
		return err
	}

	history.commands, history.timestamps = parseHistoryCells(rawLines)
	return nil
}

// appendCommand appends new command to the history.
func (history *commandHistory) appendCommand(command string) {
	history.commands = append(history.commands, command)
	history.timestamps = append(history.timestamps, time.Now().Unix())
	if len(history.commands) > history.maxCommands {
		history.commands = history.commands[1:]
		history.timestamps = history.timestamps[1:]
	}
}

// writeToFile writes console history to the file.
func (history *commandHistory) writeToFile() error {
	historyContent := bytes.Buffer{}
	for i, command := range history.commands {
		historyContent.WriteString(fmt.Sprintf("#%d\n%s\n", history.timestamps[i], command))
	}
	if err := os.WriteFile(history.filepath, historyContent.Bytes(), 0o640); err != nil {
		return fmt.Errorf("failed to write to history file: %s", err)
	}

	return nil
}

// newCommandHistory returns new commandHistory instance.
func newCommandHistory(historyFileName string, maxCommands int) (*commandHistory, error) {
	homeDir, err := util.GetHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %s", err)
	}

	history := commandHistory{
		filepath:    filepath.Join(homeDir, historyFileName),
		maxCommands: maxCommands,
	}
	return &history, nil
}
