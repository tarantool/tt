package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSplitRunArgs pins which arguments tt run reads as its own: -h and
// --help among the options before the script name, and a '--' there, which
// it drops. Everything else, and everything from the script name on, goes
// to Tarantool as typed.
func TestSplitRunArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		help bool
		argv []string
	}{
		{name: "nothing", args: []string{}, argv: []string{}},
		{name: "short help", args: []string{"-h"}, help: true},
		{name: "long help", args: []string{"--help"}, help: true},
		{name: "help after an option", args: []string{"-i", "-h"}, help: true},
		{
			name: "help after the script",
			args: []string{"script.lua", "-h"},
			argv: []string{"script.lua", "-h"},
		},
		{
			name: "help after the script and a double dash",
			args: []string{"script.lua", "--", "-h"},
			argv: []string{"script.lua", "--", "-h"},
		},
		{
			name: "help after stdin",
			args: []string{"-", "--help"},
			argv: []string{"-", "--help"},
		},
		{
			name: "double dash before the script is dropped",
			args: []string{"--", "script.lua", "a"},
			argv: []string{"script.lua", "a"},
		},
		{
			name: "double dash hands -h over to Tarantool",
			args: []string{"--", "-h"},
			argv: []string{"-h"},
		},
		{
			name: "double dash after options is dropped",
			args: []string{"-i", "--", "script.lua"},
			argv: []string{"-i", "script.lua"},
		},
		{
			name: "the value of -e is no script name",
			args: []string{"-e", "print(1)", "-h"},
			help: true,
		},
		{
			name: "the value of -e may look like help",
			args: []string{"-e", "-h", "script.lua"},
			argv: []string{"-e", "-h", "script.lua"},
		},
		{
			name: "the value of -l is no script name",
			args: []string{"-l", "fio", "script.lua", "-h"},
			argv: []string{"-l", "fio", "script.lua", "-h"},
		},
		{
			name: "an option with an attached value",
			args: []string{"--name=i1", "-h"},
			help: true,
		},
		{
			name: "everything after -b is its own",
			args: []string{"-b", "-h", "out.o"},
			argv: []string{"-b", "-h", "out.o"},
		},
		{
			name: "Tarantool options pass through",
			args: []string{"--version"},
			argv: []string{"--version"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			help, argv := splitRunArgs(tt.args)

			assert.Equal(t, tt.help, help)
			assert.Equal(t, tt.argv, argv)
		})
	}
}
