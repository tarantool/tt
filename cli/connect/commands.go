package connect

import (
	"fmt"
	"sort"
	"strings"

	"github.com/apex/log"
)

// cmd is the interface that must be implemented by a console command.
type cmd interface {
	// Aliases returns a list of all command's aliases.
	Aliases() []string
	// Run is the command run function.
	Run(console *Console, cmd string, args []string) (string, error)
}

var _ cmd = baseCmd{}
var _ cmd = noArgsCmdDecorator{}
var _ cmd = argSetCmdDecorator{}

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
	shorts := []string{"\\help, ?"}
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
	return []string{"?", "\\help"}
}

// Run runs the help command.
func (command helpCmd) Run(console *Console,
	cmd string, args []string) (string, error) {
	return command.help, nil
}

// setLanguage sets a language for the console.
func setLanguage(console *Console, cmd string, args []string) (string, error) {
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

// getShortcuts returns a list of allowed shortcuts.
func getShortcuts(console *Console, cmd string, args []string) (string, error) {
	return shortcutListText, nil
}

// cmdInfos is a list of allowed commands.
var cmdInfos = []cmdInfo{
	cmdInfo{
		Short: setLanguagePrefix + " <language>",
		Long:  "set language lua or sql",
		Cmd: newArgSetCmdDecorator(
			newBaseCmd([]string{setLanguagePrefix}, setLanguage),
			[]string{LuaLanguage.String(), SQLLanguage.String()},
		),
	},
	cmdInfo{
		Short: getShortcutsList,
		Long:  "show available hotkeys and shortcuts",
		Cmd: newNoArgsCmdDecorator(
			newBaseCmd([]string{getShortcutsList}, getShortcuts),
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

// Execute tries to interpret the input string as a command and execute it. It
// returns true if the input was a command that already executed.
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
