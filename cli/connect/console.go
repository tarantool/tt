package connect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adam-hanna/arrayOperations"
	"github.com/apex/log"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/tarantool/go-prompt"
	"github.com/tarantool/tt/cli/connect/internal/luabody"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/formatter"
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

	// Console settings.
	language   Language
	format     formatter.Format
	formatOpts formatter.Opts
	quit       bool

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
	delimiter  string

	prompt *prompt.Prompt
}

// genConsoleTitle generates console title string.
func genConsoleTitle(connOpts connector.ConnectOpts, connCtx ConnectCtx) string {
	if connCtx.ConnectTarget != "" {
		return connCtx.ConnectTarget
	}
	return connOpts.Address
}

// NewConsole creates a new console connected to the tarantool instance.
func NewConsole(connOpts connector.ConnectOpts, connectCtx ConnectCtx, title string) (*Console,
	error,
) {
	console := &Console{
		title:    title,
		connOpts: connOpts,
		language: connectCtx.Language,
		format:   connectCtx.Format,
		formatOpts: formatter.Opts{
			Graphics:       true,
			ColumnWidthMax: 0,
			TableDialect:   formatter.DefaultTableDialect,
		},
		quit: false,
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
	if connectCtx.Language != DefaultLanguage {
		if err := ChangeLanguage(console.conn, connectCtx.Language); err != nil {
			return nil, fmt.Errorf("unable to change a language: %s", err)
		}
	}

	// Initialize user commands executor.
	console.executor, err = getExecutor(console, connectCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to init prompt: %s", err)
	}

	// Initialize commands completer.
	console.completer = getCompleter(console, connectCtx)

	// Initialize syntax checkers.
	luaValidator := NewLuaValidator()
	sqlValidator := NewSQLValidator()
	console.validators = make(map[Language]ValidateCloser)
	console.validators[DefaultLanguage] = luaValidator
	console.validators[LuaLanguage] = luaValidator
	console.validators[SQLLanguage] = sqlValidator

	// Set title and prompt prefix.
	setTitle(console, genConsoleTitle(connOpts, connectCtx))
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
}

// getExecutor returns command executor.
func getExecutor(console *Console, connectCtx ConnectCtx) (func(string), error) {
	commandsExecutor := newCmdExecutor()

	evalBody, err := luabody.GetEvalFuncBody(connectCtx.Evaler)
	if err != nil {
		return nil, err
	}

	executor := func(in string) {
		if console.input == "" {
			if commandsExecutor.Execute(console, in) {
				if console.quit {
					console.Close()
					log.Infof("Quit from the console")
					os.Exit(0)
				}
				return
			}
		}

		var completed bool
		validator := console.validators[console.language]
		console.input, completed = AddStmtPart(console.input, in, console.delimiter, validator)
		if !completed {
			console.livePrefixEnabled = true
			return
		}

		trimmedInput := strings.TrimSpace(console.input)
		if console.history != nil {
			console.history.appendCommand(trimmedInput)
			if err := console.history.writeToFile(); err != nil {
				log.Debug(err.Error())
			}
		}

		if console.prompt != nil {
			if err := console.prompt.PushToHistory(trimmedInput); err != nil {
				log.Debug(err.Error())
			}
		}

		var results []string
		needMetaInfo := console.format == formatter.TableFormat ||
			console.format == formatter.TTableFormat
		args := []interface{}{
			console.input, console.language == SQLLanguage,
			needMetaInfo,
		}
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
		if _, err := console.conn.Eval(evalBody, args, opts); err != nil {
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

		output, err := formatter.MakeOutput(console.format, data, console.formatOpts)
		if err != nil {
			log.Errorf("Unable to format output: %s", err)
			log.Infof("Source YAML:\n%s", data)
		} else {
			fmt.Print(output)
		}

		console.input = ""
		console.livePrefixEnabled = false
	}

	signalable_executor := func(in string) {
		// Signal handler.
		handleSignals := func(console *Console, stop chan struct{}) {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGQUIT)
			select {
			case <-stop:
				return
			case <-sig:
				console.Close()
				os.Exit(0)
			}
		}

		stop := make(chan struct{})
		go handleSignals(console, stop)
		executor(in)
		stop <- struct{}{}
	}

	return signalable_executor, nil
}

func getCompleter(console *Console, connectCtx ConnectCtx) prompt.Completer {
	if len(connectCtx.Evaler) != 0 {
		return func(prompt.Document) []prompt.Suggest {
			return nil
		}
	}

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

		if _, err := console.conn.Eval(luabody.GetSuggestionsFuncBody(), args, opts); err != nil {
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

func setTitle(console *Console, title string) {
	if console.title != "" {
		return
	} else {
		console.title = title
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
				Fn:        prompt.GoLeftWord,
			},
			// Move to one word right.
			prompt.ASCIICodeBind{
				ASCIICode: ControlRightBytes,
				Fn:        prompt.GoRightWord,
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

		prompt.OptionDisableAutoHistory(),
		prompt.OptionReverseSearch(),
	}

	if console.history != nil {
		options = append(options, prompt.OptionHistory(console.history.commands))
	}

	return options
}
