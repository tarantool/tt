package console

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unicode"

	"github.com/apex/log"
	"golang.org/x/term"

	"github.com/tarantool/go-prompt"
)

const (
	maxLivePrefixIndent = 15
	// See https://github.com/tarantool/tarantool/blob/b53cb2aeceedc39f356ceca30bd0087ee8de7c16/src/box/lua/console.c#L265
	tarantoolWordSeparators = "\t\r\n !\"#$%&'()*+,-/;<=>?@[\\]^`{|}~"
)

var (
	controlLeftBytes  = []byte{0x1b, 0x62}
	controlRightBytes = []byte{0x1b, 0x66}
)

// ConsoleOpts collection console options to create new console.
type ConsoleOpts struct {
	// Handler is the implementation of command processor.
	Handler Handler
	// History if specified than save input commands with it.
	History HistoryKeeper
	// Format options set how to formatting result.
	Format Format
}

// Console implementation of active console handler.
type Console struct {
	impl              ConsoleOpts
	internal          Handler // internal Handler execute console's additional backslash commands.
	input             string
	quit              bool
	prefix            string
	livePrefixEnabled bool
	livePrefix        string
	delimiter         string
	prompt            *prompt.Prompt
}

// NewConsole creates a new console connected to the tarantool instance.
func NewConsole(opts ConsoleOpts) (Console, error) {
	if opts.Handler == nil {
		return Console{quit: true}, errors.New("no handler for commands has been set")
	}
	c := Console{
		impl: opts,
		quit: false,
	}
	c.setPrefix()
	return c, nil
}

func (c *Console) runOnPipe() error {
	pipe := bufio.NewScanner(os.Stdin)
	log.Infof("Processing piped input")
	for pipe.Scan() {
		line := pipe.Text()
		c.execute(line)
	}

	err := pipe.Err()
	if err == nil {
		log.Info("EOF on pipe")
	} else {
		log.Warnf("Error on pipe %v", err)
	}
	return err
}

// Run starts console.
func (c *Console) Run() error {
	if c.quit {
		return errors.New("can't run on stopped console")
	}
	if !term.IsTerminal(syscall.Stdin) {
		return c.runOnPipe()
	}

	log.Infof("Connected to %s\n", c.title())
	c.prompt = prompt.New(
		c.execute,
		c.complete,
		c.getPromptOptions()...,
	)
	c.prompt.Run()

	return nil
}

// Close frees up resources used by the console.
func (c *Console) Close() {
	c.impl.Handler.Close()
	if c.impl.History != nil {
		c.impl.History.Close()
	}
}

// executeEmbeddedCommand try process additional backslash commands.
func (c *Console) executeEmbeddedCommand(in string) bool {
	if c.input == "" && c.internal != nil {
		if c.internal.Execute(in) != nil {
			if c.quit {
				c.Close()
				log.Infof("Quit from the console")
				os.Exit(0)
			}
			return true
		}
	}
	return false
}

// cleanupDelimiter checks if the input statement ends with the string `c.delimiter`.
// If yes, it removes it. Returns true if the delimiter has been removed.
func (c *Console) cleanupDelimiter() bool {
	if c.delimiter == "" {
		return true
	}
	no_space := strings.TrimRightFunc(c.input, func(r rune) bool {
		return unicode.IsSpace(r)
	})
	no_delim := strings.TrimSuffix(no_space, c.delimiter)
	if len(no_space) > len(no_delim) {
		c.input = no_delim
		return true
	}
	return false
}

// addStmt adds a new part of the statement.
// It returns true if the statement is already completed.
func (c *Console) addStmt(part string) bool {
	if c.input == "" {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			c.input = part
		}
	} else {
		c.input += "\n" + part
	}

	has_delim := c.cleanupDelimiter()
	c.livePrefixEnabled = !has_delim || !c.impl.Handler.Validate(c.input)
	return !c.livePrefixEnabled
}

// execute called from prompt to process input.
func (c *Console) execute(in string) {
	if c.executeEmbeddedCommand(in) || !c.addStmt(in) {
		return
	}

	trimmed := strings.TrimSpace(c.input)
	if c.impl.History != nil {
		c.impl.History.AppendCommand(trimmed)
	}

	if c.prompt != nil {
		if err := c.prompt.PushToHistory(trimmed); err != nil {
			log.Debug(err.Error())
		}
	}

	results := c.impl.Handler.Execute(c.input)
	if results == nil {
		c.Close()
		log.Infof("Connection closed")
		os.Exit(0)
	}

	fmt.Println("---")
	output, err := c.impl.Format.Sprint(results)
	if err == nil {
		fmt.Println(output)
	} else {
		log.Errorf("Unable to format output: %s", err)
		log.Infof("Source results:\n%v", results)
	}

	c.input = ""
	c.livePrefixEnabled = false
}

// title return console's title.
func (c *Console) title() string {
	return c.impl.Handler.Title()
}

// complete provide prompt suggestions.
func (c *Console) complete(input prompt.Document) []prompt.Suggest {
	if c.input == "" && c.internal != nil {
		return c.internal.Complete(input)
	}
	return c.impl.Handler.Complete(input)
}

// setPrefix adjust console prefix string.
func (c *Console) setPrefix() {
	c.prefix = fmt.Sprintf("%s> ", c.title())

	livePrefixIndent := len(c.title())
	if livePrefixIndent > maxLivePrefixIndent {
		livePrefixIndent = maxLivePrefixIndent
	}

	c.livePrefix = fmt.Sprintf("%s> ", strings.Repeat(" ", livePrefixIndent))
}

// getPromptOptions prepare option for prompt.
func (c *Console) getPromptOptions() []prompt.Option {
	options := []prompt.Option{
		prompt.OptionTitle(c.title()),
		prompt.OptionPrefix(c.prefix),
		prompt.OptionLivePrefix(func() (string, bool) {
			return c.livePrefix, c.livePrefixEnabled
		}),

		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionPreviewSuggestionTextColor(prompt.DefaultColor),

		prompt.OptionCompletionWordSeparator(tarantoolWordSeparators),

		prompt.OptionAddASCIICodeBind(
			// Move to one word left.
			prompt.ASCIICodeBind{
				ASCIICode: controlLeftBytes,
				Fn:        prompt.GoLeftWord,
			},
			// Move to one word right.
			prompt.ASCIICodeBind{
				ASCIICode: controlRightBytes,
				Fn:        prompt.GoRightWord,
			},
		),
		// Interrupt current unfinished expression.
		prompt.OptionAddKeyBind(
			prompt.KeyBind{
				Key: prompt.ControlC,
				Fn: func(buf *prompt.Buffer) {
					c.input = ""
					c.livePrefixEnabled = false
					fmt.Println("^C")
				},
			},
		),

		prompt.OptionDisableAutoHistory(),
		prompt.OptionReverseSearch(),
	}

	if c.impl.History != nil {
		options = append(options, prompt.OptionHistory(c.impl.History.Commands()))
	}

	return options
}
