package connect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adam-hanna/arrayOperations"
	"github.com/apex/log"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/c-bata/go-prompt"
	"github.com/tarantool/tt/cli/connector"
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

	language Language

	history *commandHistory

	prefix            string
	livePrefixEnabled bool
	livePrefix        string
	livePrefixFunc    func() (string, bool)

	connOpts connector.ConnectOpts
	conn     connector.Connector

	executor   func(in string)
	completer  func(in prompt.Document) []prompt.Suggest
	validators map[Language]ValidateCloser

	prompt *prompt.Prompt
}

// NewConsole creates a new console connected to the tarantool instance.
func NewConsole(connOpts connector.ConnectOpts, title string, lang Language) (*Console, error) {
	console := &Console{
		title:    title,
		connOpts: connOpts,
		language: lang,
	}

	var err error

	// Initialize console history.
	console.history, err = newCommandHistory(HistoryFileName, MaxHistoryLines)
	if err == nil {
		// Load Tarantool console history from file.
		if err := console.history.load(); err != nil {
			log.Debugf("Failed to load Tarantool console history: %s", err)
		}
	} else {
		log.Debugf("Failed to initialize console history: %s", err)
	}

	// Connect to specified address.
	console.conn, err = connector.Connect(connOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %s", err)
	}

	// Change a language.
	if lang != DefaultLanguage {
		if err := ChangeLanguage(console.conn, lang); err != nil {
			return nil, fmt.Errorf("unable to change a language: %s", err)
		}
	}

	// Initialize user commands executor.
	console.executor = getExecutor(console)

	// Initialize commands completer.
	console.completer = getCompleter(console)

	// Initialize syntax checkers.
	luaValidator := NewLuaValidator()
	sqlValidator := NewSQLValidator()
	console.validators = make(map[Language]ValidateCloser)
	console.validators[DefaultLanguage] = luaValidator
	console.validators[LuaLanguage] = luaValidator
	console.validators[SQLLanguage] = sqlValidator

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
	for _, v := range console.validators {
		v.Close()
	}
	console.validators = nil
	if console.conn != nil {
		console.conn.Close()
	}

	// Sets the terminal modes to “sane” values to workaround
	// bug https://github.com/c-bata/go-prompt/issues/228
	sttySane := exec.Command("stty", "sane")
	sttySane.Stdin = os.Stdin
	_ = sttySane.Run()
}

func getExecutor(console *Console) prompt.Executor {
	executor := func(in string) {
		if console.input == "" {
			trimmed := strings.TrimSpace(in)
			if strings.HasPrefix(trimmed, setLanguagePrefix) {
				newLang := strings.TrimPrefix(trimmed, setLanguagePrefix)
				if lang, ok := ParseLanguage(newLang); ok {
					if err := ChangeLanguage(console.conn, lang); err != nil {
						log.Warnf("Failed to change language: %s", err)
					} else {
						console.language = lang
					}
				} else {
					log.Warnf("Unsupported language: %s", newLang)
				}
				return
			}
		}

		var completed bool
		validator := console.validators[console.language]
		console.input, completed = AddStmtPart(console.input, in, validator)
		if !completed {
			console.livePrefixEnabled = true
			return
		}

		if console.history != nil {
			console.history.appendCommand(strings.TrimSpace(console.input))
			if err := console.history.writeToFile(); err != nil {
				log.Debug(err.Error())
			}
		}

		var results []string
		args := []interface{}{console.input}
		opts := connector.RequestOpts{
			PushCallback: func(pushedData interface{}) {
				encodedData, err := yaml.Marshal(pushedData)
				if err != nil {
					log.Warnf("Failed to encode pushed data: %s", err)
					return
				}

				fmt.Printf("%s\n", encodedData)
			},
			ResData: &results,
		}

		var data string
		if _, err := console.conn.Eval(consoleEvalFuncBody, args, opts); err != nil {
			if err == io.EOF {
				// We need to call 'console.Close()' here because in some cases (e.g 'os.exit()')
				// it won't be called from 'defer console.Close' in 'connect.runConsole()'.
				console.Close()
				log.Fatalf("Connection was closed. Probably instance process isn't running anymore")
			} else {
				log.Fatalf("Failed to execute command: %s", err)
			}
		} else if len(results) == 0 {
			console.Close()
			log.Infof("Connection closed")
			os.Exit(0)
		} else {
			data = results[0]
		}

		fmt.Printf("%s\n", data)

		console.input = ""
		console.livePrefixEnabled = false
	}

	return executor
}

func getCompleter(console *Console) prompt.Completer {
	completer := func(in prompt.Document) []prompt.Suggest {
		if len(in.Text) == 0 {
			return nil
		}

		if console.language == SQLLanguage {
			// Tarantool does not implements auto-completion for SQL:
			// https://github.com/tarantool/tarantool/issues/2304
			return nil
		}

		lastWordStart := in.FindStartOfPreviousWordUntilSeparator(tarantoolWordSeparators)
		lastWord := in.Text[lastWordStart:]

		if len(lastWord) == 0 {
			return nil
		}

		var suggestionsTexts []string
		args := []interface{}{lastWord, len(lastWord)}
		opts := connector.RequestOpts{
			ReadTimeout: 3 * time.Second,
			ResData:     &suggestionsTexts,
		}

		if _, err := console.conn.Eval(getSuggestionsFuncBody, args, opts); err != nil {
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

	if console.history != nil {
		options = append(options, prompt.OptionHistory(console.history.commands))
	}

	return options
}
