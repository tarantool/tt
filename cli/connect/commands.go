package connect

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/go-prompt"
	"github.com/tarantool/tt/sdk/v3/formatter"
	"github.com/tarantool/tt/sdk/v3/log"
)

var (
	errTheCommandDoesNotExpectArguments      = errors.New("the command does not expect arguments")
	errTheCommandExpectsOneOf                = errors.New("the command expects one of: ")
	errTheCommandExpectsZeroOrSingleArgument = errors.New(
		"the command expects zero or single argument",
	)
	errUnsupportedDialect  = errors.New("unsupported dialect: ")
	errUnsupportedFormat   = errors.New("unsupported format: ")
	errUnsupportedLanguage = errors.New("unsupported language: ")
)

// cmd is the interface that must be implemented by a console command.
type cmd interface {
	// Aliases returns a list of all command's aliases.
	Aliases() []string
	// Run is the command run function.
	Run(console *Console, cmd string, args []string) (string, error)
}

var (
	_ cmd = baseCmd{aliases: nil, run: nil}
	_ cmd = combinedCmd{cmds: nil, aliases: nil}
	_ cmd = noArgsCmdDecorator{base: nil}
	_ cmd = argSetCmdDecorator{sorted: nil, base: nil}
	_ cmd = argUnsignedCmdDecorator{base: nil}
	_ cmd = argBooleanCmdDecorator{base: nil}
)

var (
	errNotUnsigned = errors.New("the command expects one unsigned number")
	errNotBoolean  = errors.New("the command expects one boolean")
)

// find returns true if the string is found in the sorted slice.
func find(sorted []string, str string) bool {
	_, found := slices.BinarySearch(sorted, str)

	return found
}

// runFunc is an alias for the run function.
type runFunc func(console *Console, cmd string, args []string) (string, error)

// baseCmd is a basic console command.
type baseCmd struct {
	aliases []string
	run     runFunc
}

// newBaseCmd creates a new basic console command object.
func newBaseCmd(aliases []string, run runFunc) baseCmd {
	return baseCmd{
		aliases: aliases,
		run:     run,
	}
}

// Aliases returns a list of aliases for the command.
func (command baseCmd) Aliases() []string {
	return command.aliases
}

// Run executes the command.
func (command baseCmd) Run(console *Console,
	cmd string, args []string,
) (string, error) {
	return command.run(console, cmd, args)
}

// combinedCmd is a command that combines a slice of commands into one.
type combinedCmd struct {
	cmds    map[string]cmd
	aliases []string
}

// newCombinedCmd creates a new combined command object.
func newCombinedCmd(cmds []cmd) combinedCmd {
	cmdsMap := make(map[string]cmd)

	for _, cmd := range cmds {
		for _, alias := range cmd.Aliases() {
			cmdsMap[alias] = cmd
		}
	}

	aliases := make([]string, 0, len(cmdsMap))
	for k := range cmdsMap {
		aliases = append(aliases, k)
	}

	return combinedCmd{
		cmds:    cmdsMap,
		aliases: aliases,
	}
}

// Aliases returns aliases for all commands.
func (command combinedCmd) Aliases() []string {
	return command.aliases
}

// Run picks a command based on the command string and executes it.
func (command combinedCmd) Run(console *Console,
	cmd string, args []string,
) (string, error) {
	return command.cmds[cmd].Run(console, cmd, args)
}

// noArgsCmdDecorator is a decorator for a command that checks that the
// command does not accept arguments.
type noArgsCmdDecorator struct {
	base cmd
}

// newNoArgsCmdDecorator creates a new noArgsCmdDecorator object.
func newNoArgsCmdDecorator(base cmd) noArgsCmdDecorator {
	return noArgsCmdDecorator{
		base: base,
	}
}

// Aliases returns aliases of the base command.
func (command noArgsCmdDecorator) Aliases() []string {
	return command.base.Aliases()
}

// Run checks that there is no arguments and runs the command.
func (command noArgsCmdDecorator) Run(console *Console,
	cmd string, args []string,
) (string, error) {
	if len(args) != 0 {
		return "", errTheCommandDoesNotExpectArguments
	}

	return command.base.Run(console, cmd, args)
}

// argSetCmdDecorator is a decorator for a command that checks that a
// first and only argument in the set of allowed arguments.
type argSetCmdDecorator struct {
	sorted []string
	base   cmd
}

// newArgSetCmdDecorator creates a new argSetCmdDecorator object from a base
// command and a set of allowed arguments.
func newArgSetCmdDecorator(base cmd, allowed []string) argSetCmdDecorator {
	sorted := slices.Clone(allowed)
	slices.Sort(sorted)

	return argSetCmdDecorator{
		sorted: sorted,
		base:   base,
	}
}

// Aliases returns aliases of the base command.
func (command argSetCmdDecorator) Aliases() []string {
	return command.base.Aliases()
}

// Run checks that there is one allowed argument and runs the command.
func (command argSetCmdDecorator) Run(console *Console,
	cmd string, args []string,
) (string, error) {
	if len(command.sorted) > 0 && (len(args) != 1 || !find(command.sorted, args[0])) {
		return "", fmt.Errorf("%w%s",
			errTheCommandExpectsOneOf, strings.Join(command.sorted, ", "))
	}

	return command.base.Run(console, cmd, args)
}

// Args returns the arguments the command takes, sorted.
func (command argSetCmdDecorator) Args() []string {
	return command.sorted
}

// argUnsignedDecorator is a decorator for a command that checks that a
// first and only argument is a positive integer.
type argUnsignedCmdDecorator struct {
	base cmd
}

// newArgUnsignedCmdDecorator creates a new argUnsignedCmdDecorator object
// from a base command.
func newArgUnsignedCmdDecorator(base cmd) argUnsignedCmdDecorator {
	return argUnsignedCmdDecorator{
		base: base,
	}
}

// Aliases returns aliases of the base command.
func (command argUnsignedCmdDecorator) Aliases() []string {
	return command.base.Aliases()
}

// Run checks that there is one unsigned number argument and runs the command.
func (command argUnsignedCmdDecorator) Run(console *Console,
	cmd string, args []string,
) (string, error) {
	if len(args) != 1 {
		return "", errNotUnsigned
	}

	_, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return "", errNotUnsigned
	}

	return command.base.Run(console, cmd, args)
}

// argBooleanDecorator is a decorator for a command that checks that a
// first and only argument is a boolean.
type argBooleanCmdDecorator struct {
	base cmd
}

// newArgBooleanCmdDecorator creates a new argBooleanCmdDecorator object
// from a base command.
func newArgBooleanCmdDecorator(base cmd) argBooleanCmdDecorator {
	return argBooleanCmdDecorator{
		base: base,
	}
}

// Aliases returns aliases of the base command.
func (command argBooleanCmdDecorator) Aliases() []string {
	return command.base.Aliases()
}

// Run checks that there is one boolean argument and runs the command.
func (command argBooleanCmdDecorator) Run(console *Console,
	cmd string, args []string,
) (string, error) {
	if len(args) != 1 {
		return "", errNotBoolean
	}

	_, err := strconv.ParseBool(args[0])
	if err != nil {
		return "", errNotBoolean
	}

	return command.base.Run(console, cmd, args)
}

// Args returns the arguments the command is completed with.
func (command argBooleanCmdDecorator) Args() []string {
	return []string{"false", "true"}
}

// argsLister is a command that takes one of a set of arguments, which the
// console completes.
type argsLister interface {
	// Args returns the arguments, sorted.
	Args() []string
}

// cmdInfo describes an additional information about a command.
type cmdInfo struct {
	// Short is a short help description for the command.
	Short string
	// Long is a long help description for the command.
	Long string
	// Cmd is the described command itself.
	Cmd cmd
}

// helpDescription describes the help command.
const helpDescription = "show this screen"

// helpCmd is a special type of a console command that shows the help for
// other commands.
type helpCmd struct {
	// The help message.
	help string
}

// newHelpCmd creates a new helpCmd object.
func newHelpCmd(infos []cmdInfo) helpCmd {
	shorts := make([]string, 0, 1+len(infos))

	shorts = append(shorts, strings.Join(getHelp, ", "))

	longs := make([]string, 0, 1+len(infos))

	longs = append(longs, helpDescription)

	var msg strings.Builder

	msg.WriteString(`
  To get help, see the Tarantool manual at https://tarantool.io/en/doc/
  To start the interactive Tarantool tutorial, type 'tutorial()' here.

  tt connect runs the backslash commands itself and adds some of its own.

  Available backslash commands:

`)

	shortMaxLen := len(shorts[0])
	for _, info := range infos {
		if len(info.Short) > shortMaxLen {
			shortMaxLen = len(info.Short)
		}

		shorts = append(shorts, info.Short)
		longs = append(longs, info.Long)
	}

	for idx := range shorts {
		msg.WriteString("  ")
		msg.WriteString(shorts[idx])

		for j := len(shorts[idx]); j < shortMaxLen; j++ {
			msg.WriteByte(' ')
		}

		msg.WriteString(" -- ")
		msg.WriteString(longs[idx])
		msg.WriteByte('\n')
	}

	return helpCmd{
		help: msg.String(),
	}
}

// Aliases returns a list of aliases for the help command.
func (command helpCmd) Aliases() []string {
	return getHelp
}

// Run runs the help command.
func (command helpCmd) Run(console *Console,
	cmd string, args []string,
) (string, error) {
	return command.help, nil
}

// setLanguageFunc sets a language for the console.
func setLanguageFunc(console *Console, cmd string, args []string) (string, error) {
	lang, ok := ParseLanguage(args[0])
	if !ok {
		return "", fmt.Errorf("%w%s", errUnsupportedLanguage, args[0])
	}

	err := ChangeLanguage(console.conn, lang)
	if err != nil {
		return "", fmt.Errorf("failed to change language: %w", err)
	}

	console.language = lang

	return "", nil
}

// setFormatFunc sets the output format for the console.
func setFormatFunc(console *Console, cmd string, args []string) (string, error) {
	formatStr := args[0]
	if newFormat, ok := formatter.ParseFormat(formatStr); ok {
		console.format = newFormat
	} else {
		// It should not happen in practice.
		return "", fmt.Errorf("%w%s", errUnsupportedFormat, formatStr)
	}

	return "", nil
}

// setTableDialectFunc sets the table dialect for the console.
func setTableDialectFunc(console *Console, cmd string, args []string) (string, error) {
	dialectStr := args[0]
	if dialect, ok := formatter.ParseTableDialect(dialectStr); ok {
		console.formatOpts.TableDialect = dialect
	} else {
		// It should not happen in practice.
		return "", fmt.Errorf("%w%s", errUnsupportedDialect, dialectStr)
	}

	return "", nil
}

// setGraphicsFunc sets the graphics mode on/off.
func setGraphicsFunc(console *Console,
	cmd string, args []string,
) (string, error) {
	val, err := strconv.ParseBool(args[0])
	if err != nil {
		// It should not happen in practice.
		return "", fmt.Errorf("parsing error: %w", err)
	}

	console.formatOpts.Graphics = val

	return "", nil
}

// setMaxTableWidthFunc sets the maximum table width for the console.
func setTableColumnWidthMaxFunc(console *Console,
	cmd string, args []string,
) (string, error) {
	val, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		// It should not happen in practice.
		return "", fmt.Errorf("parsing error: %w", err)
	}

	//nolint:gosec // The argument is a user-given width; values above MaxInt are not meaningful.
	console.formatOpts.ColumnWidthMax = int(val)

	return "", nil
}

// setDelimiterMarker apply delimiter to the Console object.
func setDelimiterMarker(console *Console, cmd string, args []string) (string, error) {
	switch len(args) {
	case 0:
		console.delimiter = ""
	case 1:
		console.delimiter = args[0]
	default:
		return "", errTheCommandExpectsZeroOrSingleArgument
	}

	return "", nil
}

// switchNextFormatFunc switches to a next output format.
func switchNextFormatFunc(console *Console, cmd string, args []string) (string, error) {
	console.format = (1 + console.format) % formatter.FormatsAmount
	return "", nil
}

// getSetFormatFunc returns a function to set up a specified format.
func getSetFormatFunc(format formatter.Format) runFunc {
	return func(console *Console, cmd string, args []string) (string, error) {
		console.format = format
		return "", nil
	}
}

// enableGraphics enables graphics output.
func enableGraphicsFunc(console *Console, cmd string, args []string) (string, error) {
	console.formatOpts.Graphics = true
	return "", nil
}

// disableGraphics disables graphics output.
func disableGraphicsFunc(console *Console, cmd string, args []string) (string, error) {
	console.formatOpts.Graphics = false
	return "", nil
}

// getShortcuts returns a list of allowed shortcuts.
func getShortcutsFunc(console *Console, cmd string, args []string) (string, error) {
	return shortcutListText, nil
}

// getHistoryFunc returns the last executed commands: as many as its argument
// says, and DefaultOutputHistorySize without one.
func getHistoryFunc(console *Console, _ string, args []string) (string, error) {
	limit := DefaultOutputHistorySize

	switch len(args) {
	case 0:
	case 1:
		count, err := strconv.Atoi(args[0])
		if err != nil || count < 0 {
			return "", errNotUnsigned
		}

		limit = count
	default:
		return "", errTheCommandExpectsZeroOrSingleArgument
	}

	if console.history == nil || len(console.history.commands) == 0 {
		return "", nil
	}

	outputData := console.history.commands
	if len(outputData) > limit {
		outputData = outputData[len(outputData)-limit:]
	}

	return strings.Join(outputData, "\n-----\n"), nil
}

// setQuitFunc sets the quit flag for the console.
func setQuitFunc(console *Console, cmd string, arg []string) (string, error) {
	console.quit = true
	return "", nil
}

// cmdInfos is a list of allowed commands.
var cmdInfos = []cmdInfo{
	{
		Short: setLanguage + " <language>",
		Long:  "set language lua (default) or sql",
		Cmd: newArgSetCmdDecorator(
			newBaseCmd([]string{setLanguage}, setLanguageFunc),
			[]string{LuaLanguage.String(), SQLLanguage.String()},
		),
	},
	{
		Short: setFormatLong + " <format>",
		Long:  "set output format " + FormatsText(true),
		Cmd: newArgSetCmdDecorator(
			newBaseCmd([]string{setFormatLong}, setFormatFunc),
			formatNames(),
		),
	},
	{
		Short: setTableDialect + " <format>",
		Long:  "set table format default, jira or markdown",
		Cmd: newArgSetCmdDecorator(
			newBaseCmd([]string{setTableDialect}, setTableDialectFunc),
			[]string{
				formatter.DefaultTableDialect.String(),
				formatter.JiraTableDialect.String(),
				formatter.MarkdownTableDialect.String(),
			},
		),
	},
	{
		Short: setGraphics + " <false/true>",
		Long:  "disables/enables pseudographics for table modes",
		Cmd: newArgBooleanCmdDecorator(
			newBaseCmd(
				[]string{setGraphics},
				setGraphicsFunc,
			),
		),
	},
	{
		Short: setTableColumnWidthMaxLong + " <width>",
		Long:  "set max column width for table/ttable",
		Cmd: newArgUnsignedCmdDecorator(
			newBaseCmd(
				[]string{setTableColumnWidthMaxLong},
				setTableColumnWidthMaxFunc,
			),
		),
	},
	{
		Short: setDelimiter + " <marker>",
		Long:  "set expression delimiter",
		Cmd: newArgSetCmdDecorator(
			newBaseCmd([]string{setDelimiter}, setDelimiterMarker),
			[]string{},
		),
	},
	{
		Short: setTableColumnWidthMaxShort + " <width>",
		Long:  "set max column width for table/ttable",
		Cmd: newArgUnsignedCmdDecorator(
			newBaseCmd(
				[]string{setTableColumnWidthMaxShort},
				setTableColumnWidthMaxFunc,
			),
		),
	},
	{
		Short: setNextFormat,
		Long:  "switches output format cyclically",
		Cmd: newNoArgsCmdDecorator(
			newBaseCmd(
				[]string{setNextFormat},
				switchNextFormatFunc,
			),
		),
	},
	{
		Short: formatShortcutsText(),
		Long:  "set output format " + FormatsText(false),
		Cmd:   newCombinedCmd(formatShortcutCmds()),
	},
	{
		Short: "\\x[g,G]",
		Long:  "disables/enables pseudographics for table modes",
		Cmd: newCombinedCmd([]cmd{
			newNoArgsCmdDecorator(
				newBaseCmd(
					[]string{setGraphicsEnable},
					enableGraphicsFunc,
				),
			),
			newNoArgsCmdDecorator(
				newBaseCmd(
					[]string{setGraphicsDisable},
					disableGraphicsFunc,
				),
			),
		}),
	},
	{
		Short: getShortcutsList,
		Long:  "show available hotkeys and shortcuts",
		Cmd: newNoArgsCmdDecorator(
			newBaseCmd([]string{getShortcutsList}, getShortcutsFunc),
		),
	},
	{
		Short: getHistoryList + " [<count>]",
		Long:  "show the last <count> executed commands (default 10)",
		Cmd:   newBaseCmd([]string{getHistoryList}, getHistoryFunc),
	},
	// The Tarantool console has `\quit` command, but it requires execute
	// access.
	{
		Short: strings.Join(setQuit, ", "),
		Long:  "quit from the console",
		Cmd: newNoArgsCmdDecorator(
			newBaseCmd(setQuit, setQuitFunc),
		),
	},
}

// cmdExecutor executes console commands.
type cmdExecutor struct {
	cmds map[string]cmd
	// aliases are the names of the commands, sorted.
	aliases []string
	// descriptions describe the commands by their names, as the help does.
	descriptions map[string]string
}

// newCmdExecutor creates a new command executor object.
func newCmdExecutor() cmdExecutor {
	// Create a full list of console commands.
	helpCmd := newNoArgsCmdDecorator(newHelpCmd(cmdInfos))
	cmds := make([]cmd, 0, 1+len(cmdInfos))

	cmds = append(cmds, helpCmd)

	for _, info := range cmdInfos {
		cmds = append(cmds, info.Cmd)
	}

	// Create a map of commands.
	cmdsMap := make(map[string]cmd)

	for _, cmd := range cmds {
		for _, alias := range cmd.Aliases() {
			cmdsMap[alias] = cmd
		}
	}

	descriptions := make(map[string]string)
	for _, alias := range helpCmd.Aliases() {
		descriptions[alias] = helpDescription
	}

	for _, info := range cmdInfos {
		for _, alias := range info.Cmd.Aliases() {
			descriptions[alias] = info.Long
		}
	}

	return cmdExecutor{
		cmds:         cmdsMap,
		aliases:      slices.Sorted(maps.Keys(cmdsMap)),
		descriptions: descriptions,
	}
}

// Complete returns the completions of text, the input before the cursor,
// and true if text is a backslash command: the commands it is the start of,
// or the arguments of the command it names. A completion replaces word, the
// end of text after the last separator of words, which the completion keeps
// in front of the cursor.
func (executor cmdExecutor) Complete(text, word string) ([]prompt.Suggest, bool) {
	text = strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(text, "\\") || !strings.HasSuffix(text, word) {
		return nil, false
	}

	// The part of text no completion replaces.
	kept := len(text) - len(word)

	var suggests []prompt.Suggest

	for _, alias := range executor.aliases {
		if len(alias) > len(text) && strings.HasPrefix(alias, text) {
			suggests = append(suggests, prompt.Suggest{
				Text:        alias[kept:],
				Description: executor.descriptions[alias],
			})
		}

		lister, ok := executor.cmds[alias].(argsLister)
		if !ok || !strings.HasPrefix(text, alias+" ") {
			continue
		}

		// The argument being typed: the command takes one.
		arg := strings.TrimLeft(text[len(alias):], " ")
		if strings.Contains(arg, " ") || len(arg) < len(word) {
			continue
		}

		for _, value := range lister.Args() {
			if len(value) > len(arg) && strings.HasPrefix(value, strings.ToLower(arg)) {
				suggests = append(suggests, prompt.Suggest{
					Text:        value[len(arg)-len(word):],
					Description: "",
				})
			}
		}
	}

	return suggests, true
}

// Execute attempts to interpret the input string as a command and execute it.
// It returns true if the input has already been executed as a command.
func (executor cmdExecutor) Execute(console *Console, in string) bool {
	dirtyTokens := strings.Split(strings.TrimSpace(in), " ")

	tokens := []string{}
	lowerTokens := []string{}

	for _, token := range dirtyTokens {
		token = strings.Trim(token, " ")
		if token != "" {
			tokens = append(tokens, token)
			lowerTokens = append(lowerTokens, strings.ToLower(token))
		}
	}

	for i := len(tokens); i > 0; i-- {
		key := strings.Join(tokens[:i], " ")
		if cmd, ok := executor.cmds[key]; ok {
			msg, err := cmd.Run(console, key, lowerTokens[i:])
			if err != nil {
				log.Errorf("%s\n", err)
			} else if msg != "" {
				_, _ = fmt.Fprintf(os.Stdout, "%s\n", msg)
			}

			return true
		}
	}

	return false
}
