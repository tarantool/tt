package connect

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/apex/log"

	"github.com/tarantool/tt/cli/formatter"
)

// cmd is the interface that must be implemented by a console command.
type cmd interface {
	// Aliases returns a list of all command's aliases.
	Aliases() []string
	// Run is the command run function.
	Run(console *Console, cmd string, args []string) (string, error)
}

var (
	_ cmd = baseCmd{}
	_ cmd = combinedCmd{}
	_ cmd = noArgsCmdDecorator{}
	_ cmd = argSetCmdDecorator{}
	_ cmd = argUnsignedCmdDecorator{}
	_ cmd = argBooleanCmdDecorator{}
)

var (
	errNotUnsigned = errors.New("the command expects one unsigned number")
	errNotBoolean  = errors.New("the command expects one boolean")
)

// find returns true if the string is found in the sorted slice.
func find(sorted []string, str string) bool {
	idx := sort.SearchStrings(sorted, str)
	return idx != len(sorted) && str == sorted[idx]
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
	cmd string, args []string) (string, error) {
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
	cmd string, args []string) (string, error) {
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
	cmd string, args []string) (string, error) {
	if len(args) != 0 {
		return "", fmt.Errorf("the command does not expect arguments")
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
	sorted := make([]string, len(allowed))
	copy(sorted, allowed)
	sort.Strings(sorted)

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
	cmd string, args []string) (string, error) {
	if len(args) != 1 || !find(command.sorted, args[0]) {
		return "", fmt.Errorf("the command expects one of: %s",
			strings.Join(command.sorted, ", "))
	}

	return command.base.Run(console, cmd, args)
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
	cmd string, args []string) (string, error) {
	if len(args) != 1 {
		return "", errNotUnsigned
	}
	if _, err := strconv.ParseUint(args[0], 10, 64); err != nil {
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
	cmd string, args []string) (string, error) {
	if len(args) != 1 {
		return "", errNotBoolean
	}
	if _, err := strconv.ParseBool(args[0]); err != nil {
		return "", errNotBoolean
	}

	return command.base.Run(console, cmd, args)
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

// helpCmd is a special type of a console command that shows the help for
// other commands.
type helpCmd struct {
	// The help message.
	help string
}

// newHelpCmd creates a new helpCmd object.
func newHelpCmd(infos []cmdInfo) helpCmd {
	shorts := []string{strings.Join(getHelp, ", ")}
	longs := []string{"show this screen"}
	msg := `
  To get help, see the Tarantool manual at https://tarantool.io/en/doc/
  To start the interactive Tarantool tutorial, type 'tutorial()' here.

  This help is expanded with additional backslash commands
  because tt connect is using.

  Available backslash commands:

`

	shortMaxLen := len(shorts[0])
	for _, info := range infos {
		if len(info.Short) > shortMaxLen {
			shortMaxLen = len(info.Short)
		}
		shorts = append(shorts, info.Short)
		longs = append(longs, info.Long)
	}

	for i := 0; i < len(shorts); i++ {
		msg += "  " + shorts[i]
		for j := len(shorts[i]); j < shortMaxLen; j++ {
			msg += " "
		}
		msg += " -- " + longs[i] + "\n"
	}

	return helpCmd{
		help: msg,
	}
}

// Aliases returns a list of aliases for the help command.
func (command helpCmd) Aliases() []string {
	return getHelp
}

// Run runs the help command.
func (command helpCmd) Run(console *Console,
	cmd string, args []string) (string, error) {
	return command.help, nil
}

// setLanguageFunc sets a language for the console.
func setLanguageFunc(console *Console, cmd string, args []string) (string, error) {
	if lang, ok := ParseLanguage(args[0]); ok {
		if err := ChangeLanguage(console.conn, lang); err != nil {
			return "", fmt.Errorf("failed to change language: %s", err)
		} else {
			console.language = lang
		}
	} else {
		return "", fmt.Errorf("unsupported language: %s", args[0])
	}
	return "", nil
}

// setFormatFunc sets the output format for the console.
func setFormatFunc(console *Console, cmd string, args []string) (string, error) {
	formatStr := args[0]
	if newFormat, ok := formatter.ParseFormat(formatStr); ok {
		console.format = newFormat
	} else {
		// It should not happen in practice.
		return "", fmt.Errorf("unsupported format: %s", formatStr)
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
		return "", fmt.Errorf("unsupported dialect: %s", dialectStr)
	}
	return "", nil
}

// setGraphicsFunc sets the graphics mode on/off.
func setGraphicsFunc(console *Console,
	cmd string, args []string) (string, error) {
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
	cmd string, args []string) (string, error) {
	val, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		// It should not happen in practice.
		return "", fmt.Errorf("parsing error: %w", err)
	}

	console.formatOpts.ColumnWidthMax = int(val)
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

// setQuitFunc sets the quit flag for the console.
func setQuitFunc(console *Console, cmd string, arg []string) (string, error) {
	console.quit = true
	return "", nil
}

// cmdInfos is a list of allowed commands.
var cmdInfos = []cmdInfo{
	cmdInfo{
		Short: setLanguage + " <language>",
		Long:  "set language lua (default) or sql",
		Cmd: newArgSetCmdDecorator(
			newBaseCmd([]string{setLanguage}, setLanguageFunc),
			[]string{LuaLanguage.String(), SQLLanguage.String()},
		),
	},
	cmdInfo{
		Short: setFormatLong + " <format>",
		Long:  "set format lua, table, ttable or yaml (default)",
		Cmd: newArgSetCmdDecorator(
			newBaseCmd([]string{setFormatLong}, setFormatFunc),
			[]string{
				formatter.LuaFormat.String(),
				formatter.TableFormat.String(),
				formatter.TTableFormat.String(),
				formatter.YamlFormat.String(),
			},
		),
	},
	cmdInfo{
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
	cmdInfo{
		Short: setGraphics + " <false/true>",
		Long:  "disables/enables pseudographics for table modes",
		Cmd: newArgBooleanCmdDecorator(
			newBaseCmd(
				[]string{setGraphics},
				setGraphicsFunc,
			),
		),
	},
	cmdInfo{
		Short: setTableColumnWidthMaxLong + " <width>",
		Long:  "set max column width for table/ttable",
		Cmd: newArgUnsignedCmdDecorator(
			newBaseCmd(
				[]string{setTableColumnWidthMaxLong},
				setTableColumnWidthMaxFunc,
			),
		),
	},
	cmdInfo{
		Short: setTableColumnWidthMaxShort + " <width>",
		Long:  "set max column width for table/ttable",
		Cmd: newArgUnsignedCmdDecorator(
			newBaseCmd(
				[]string{setTableColumnWidthMaxShort},
				setTableColumnWidthMaxFunc,
			),
		),
	},
	cmdInfo{
		Short: setNextFormat,
		Long:  "switches output format cyclically",
		Cmd: newNoArgsCmdDecorator(
			newBaseCmd(
				[]string{setNextFormat},
				switchNextFormatFunc,
			),
		),
	},
	cmdInfo{
		Short: "\\x[l,t,T,y]",
		Long:  "set output format lua, table, ttable or yaml",
		Cmd: newCombinedCmd([]cmd{
			newNoArgsCmdDecorator(
				newBaseCmd(
					[]string{setFormatLua},
					getSetFormatFunc(formatter.LuaFormat),
				),
			),
			newNoArgsCmdDecorator(
				newBaseCmd(
					[]string{setFormatTable},
					getSetFormatFunc(formatter.TableFormat),
				),
			),
			newNoArgsCmdDecorator(
				newBaseCmd(
					[]string{setFormatTTable},
					getSetFormatFunc(formatter.TTableFormat),
				),
			),
			newNoArgsCmdDecorator(
				newBaseCmd(
					[]string{setFormatYaml},
					getSetFormatFunc(formatter.YamlFormat),
				),
			),
		}),
	},
	cmdInfo{
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
	cmdInfo{
		Short: getShortcutsList,
		Long:  "show available hotkeys and shortcuts",
		Cmd: newNoArgsCmdDecorator(
			newBaseCmd([]string{getShortcutsList}, getShortcutsFunc),
		),
	},
	// The Tarantool console has `\quit` command, but it requires execute
	// access.
	cmdInfo{
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
}

// newCmdExecutor creates a new command executor object.
func newCmdExecutor() cmdExecutor {
	// Create a full list of console commands.
	helpCmd := newNoArgsCmdDecorator(newHelpCmd(cmdInfos))
	cmds := []cmd{helpCmd}
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

	return cmdExecutor{
		cmds: cmdsMap,
	}
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
				fmt.Printf("%s\n", msg)
			}
			return true
		}
	}

	return false
}
