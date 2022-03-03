package connect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adam-hanna/arrayOperations"
	"github.com/apex/log"
	lua "github.com/yuin/gopher-lua"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/c-bata/go-prompt"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/util"
)

// EvalFunc defines a function type for evaluating an expression via connection.
type EvalFunc func(console *Console, funcBodyFmt string, args ...interface{}) (interface{}, error)

const (
	HistoryFileName = ".tarantool_history"

	MaxLivePrefixIndent = 15
	MaxHistoryLines     = 10000
)

var (
	ControlLeftBytes  []byte
	ControlRightBytes []byte
)

func init() {
	ControlLeftBytes = []byte{0x1b, 0x62}
	ControlRightBytes = []byte{0x1b, 0x66}
}

// Console describes the console connected to the tarantool instance.
type Console struct {
	input string

	title string

	historyFile     *os.File
	historyFilePath string
	historyLines    []string

	prefix            string
	livePrefixEnabled bool
	livePrefix        string
	livePrefixFunc    func() (string, bool)

	connOpts *connector.ConnOpts
	conn     *connector.Conn

	executor  func(in string)
	completer func(in prompt.Document) []prompt.Suggest

	luaState *lua.LState

	prompt *prompt.Prompt
}

// NewConsole creates a new console connected to the tarantool instance.
func NewConsole(connOpts *connector.ConnOpts, title string) (*Console, error) {
	console := &Console{
		title:    title,
		connOpts: connOpts,
		luaState: lua.NewState(),
	}

	var err error

	// Load Tarantool console history from file.
	if err := loadHistory(console); err != nil {
		log.Debugf("Failed to load Tarantool console history: %s", err)
	}

	// Connect to specified address.
	console.conn, err = connector.Connect(connOpts.Address, connOpts.Username, connOpts.Password)
	if err != nil {
		return nil, fmt.Errorf("Failed to connect: %s", err)
	}

	// Initialize user commands executor.
	console.executor = getExecutor(console)

	// Initialize commands completer.
	console.completer = getCompleter(console)

	// Set title and prompt prefix.
	setTitle(console)
	setPrefix(console)

	return console, nil
}

// Run starts console.
func (console *Console) Run() error {
	if !terminal.IsTerminal(syscall.Stdin) {
		log.Debugf("Found piped input")
		pipedInputScanner := bufio.NewScanner(os.Stdin)
		for pipedInputScanner.Scan() {
			line := pipedInputScanner.Text()
			console.executor(line)
		}
		return nil
	} else {
		log.Infof("Connected to %s\n", console.title)
	}

	// Get options for Prompt instance.
	options := getPromptOptions(console)

	// Create Prompt instance.
	console.prompt = prompt.New(
		console.executor,
		console.completer,
		options...,
	)

	console.prompt.Run()

	return nil
}

// Close frees up resources used by the console.
func (console *Console) Close() {
	if console.historyFile != nil {
		console.historyFile.Close()
	}
}

func loadHistory(console *Console) error {
	var err error

	homeDir, err := util.GetHomeDir()
	if err != nil {
		return fmt.Errorf("Failed to get home directory: %s", err)
	}

	console.historyFilePath = filepath.Join(homeDir, HistoryFileName)

	console.historyLines, err = util.GetLastNLines(console.historyFilePath, MaxHistoryLines)
	if err != nil {
		return fmt.Errorf("Failed to read history from file: %s", err)
	}

	// Open history file for appending.
	// See https://unix.stackexchange.com/questions/346062/concurrent-writing-to-a-log-file-from-many-processes
	console.historyFile, err = os.OpenFile(
		console.historyFilePath,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)

	if err != nil {
		log.Debugf("Failed to open history file for append: %s", err)
	}

	return nil
}

func getExecutor(console *Console) prompt.Executor {
	executor := func(in string) {
		console.input += in + " "

		if !inputIsCompleted(console.input, console.luaState) {
			console.livePrefixEnabled = true
			return
		}

		if err := appendToHistoryFile(console, strings.TrimSpace(console.input)); err != nil {
			log.Debugf("Failed to append command to history file: %s", err)
		}

		req := connector.EvalReq(evalFuncBody, console.input)
		req.SetPushCallback(func(pushedData interface{}) {
			encodedData, err := yaml.Marshal(pushedData)
			if err != nil {
				log.Warnf("Failed to encode pushed data: %s", err)
				return
			}

			fmt.Printf("%s\n", encodedData)
		})

		var data string
		var results []string
		if err := console.conn.ExecTyped(req, &results); err != nil {
			if err == io.EOF {
				log.Fatalf("Connection was closed. Probably instance process isn't running anymore")
			} else {
				log.Fatalf("Failed to execute command: %s", err)
			}
		} else {
			data = results[0]
		}

		fmt.Printf("%s\n", data)

		console.input = ""
		console.livePrefixEnabled = false
	}

	return executor
}

func inputIsCompleted(input string, luaState *lua.LState) bool {
	// See https://github.com/tarantool/tarantool/blob/b53cb2aeceedc39f356ceca30bd0087ee8de7c16/src/box/lua/console.lua#L575
	if _, err := luaState.LoadString(input); err == nil || !strings.Contains(err.Error(), "at EOF") {
		// Valid Lua code or a syntax error not due to an incomplete input.
		return true
	}

	if _, err := luaState.LoadString(fmt.Sprintf("return %s", input)); err == nil {
		// Certain obscure inputs like '(42\n)' yield the same error as incomplete statement.
		return true
	}

	return false
}

func getCompleter(console *Console) prompt.Completer {
	completer := func(in prompt.Document) []prompt.Suggest {
		if len(in.Text) == 0 {
			return nil
		}

		lastWordStart := in.FindStartOfPreviousWordUntilSeparator(tarantoolWordSeparators)
		lastWord := in.Text[lastWordStart:]

		if len(lastWord) == 0 {
			return nil
		}

		req := connector.EvalReq(getSuggestionsFuncBody, lastWord, len(lastWord))
		req.SetReadTimeout(3 * time.Second)

		var suggestionsTexts []string
		if err := console.conn.ExecTyped(req, &suggestionsTexts); err != nil {
			return nil
		}

		suggestionsTexts = arrayOperations.DifferenceString(suggestionsTexts)
		if len(suggestionsTexts) == 0 {
			return nil
		}

		sort.Strings(suggestionsTexts)

		suggestions := make([]prompt.Suggest, len(suggestionsTexts))
		for i, suggestionText := range suggestionsTexts {
			suggestions[i] = prompt.Suggest{
				Text: suggestionText,
			}
		}

		return suggestions
	}

	return completer
}

func setTitle(console *Console) {
	if console.title != "" {
		return
	} else {
		console.title = console.connOpts.Address
	}
}

func setPrefix(console *Console) {
	console.prefix = fmt.Sprintf("%s> ", console.title)

	livePrefixIndent := len(console.title)
	if livePrefixIndent > MaxLivePrefixIndent {
		livePrefixIndent = MaxLivePrefixIndent
	}

	console.livePrefix = fmt.Sprintf("%s> ", strings.Repeat(" ", livePrefixIndent))

	console.livePrefixFunc = func() (string, bool) {
		return console.livePrefix, console.livePrefixEnabled
	}
}

func getPromptOptions(console *Console) []prompt.Option {
	options := []prompt.Option{
		prompt.OptionTitle(console.title),
		prompt.OptionPrefix(console.prefix),
		prompt.OptionLivePrefix(console.livePrefixFunc),

		prompt.OptionHistory(console.historyLines),

		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionPreviewSuggestionTextColor(prompt.DefaultColor),

		prompt.OptionCompletionWordSeparator(tarantoolWordSeparators),

		prompt.OptionAddASCIICodeBind(
			// Move to one word left.
			prompt.ASCIICodeBind{
				ASCIICode: ControlLeftBytes,
				Fn: func(buf *prompt.Buffer) {
					d := buf.Document()
					wordLen := len([]rune(d.GetWordBeforeCursorWithSpace()))
					buf.CursorLeft(wordLen)
				},
			},
			// Move to one word right.
			prompt.ASCIICodeBind{
				ASCIICode: ControlRightBytes,
				Fn: func(buf *prompt.Buffer) {
					d := buf.Document()
					wordLen := len([]rune(d.GetWordAfterCursorWithSpace()))
					buf.CursorRight(wordLen)
				},
			},
		),
		// Interrupt current unfinished expression.
		prompt.OptionAddKeyBind(
			prompt.KeyBind{
				Key: prompt.ControlC,
				Fn: func(buf *prompt.Buffer) {
					console.input = ""
					console.livePrefixEnabled = false
					fmt.Println("^C")
				},
			},
		),
	}

	return options
}

func appendToHistoryFile(console *Console, in string) error {
	if console.historyFile == nil {
		return fmt.Errorf("No hostory file found")
	}

	if _, err := console.historyFile.WriteString(in + "\n"); err != nil {
		return fmt.Errorf("Failed to append to history file: %s", err)
	}

	if err := console.historyFile.Sync(); err != nil {
		return fmt.Errorf("Failed to sync history file: %s", err)
	}

	return nil
}
