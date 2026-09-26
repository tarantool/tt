package mount_test

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/core/internal/mount"
)

// errFailed is what the commands of these tests fail with.
var errFailed = errors.New("failed")

// leaf returns a runnable command.
func leaf(name string, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use:     name,
		Aliases: aliases,
		RunE:    func(*cobra.Command, []string) error { return nil },
	}
}

// group returns a group, not runnable itself, of subs.
func group(name string, subs ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{Use: name}
	cmd.AddCommand(subs...)

	return cmd
}

// withAliases sets cmd's aliases.
func withAliases(cmd *cobra.Command, aliases ...string) *cobra.Command {
	cmd.Aliases = aliases

	return cmd
}

// withFlag declares a bool flag on cmd, persistent when persistent is set.
func withFlag(cmd *cobra.Command, name, shorthand string, persistent bool) *cobra.Command {
	flags := cmd.Flags()
	if persistent {
		flags = cmd.PersistentFlags()
	}

	flags.BoolP(name, shorthand, false, "")

	return cmd
}

// newRoot returns a root with a flag like tt's.
func newRoot() *cobra.Command {
	root := &cobra.Command{Use: "tt"}
	root.Flags().BoolP("verbose", "V", false, "")

	return root
}

// reserved returns what tt reserves.
func reserved() mount.Reserved {
	flags := pflag.NewFlagSet("reserved", pflag.ContinueOnError)
	flags.BoolP("help", "h", false, "")
	flags.StringP("directory", "C", "", "")

	return mount.Reserved{
		Names: []string{"help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd},
		Flags: flags,
	}
}

// render lists the tree under root, one command per line with its owner,
// and marks placeholders.
func render(root *cobra.Command, registry *mount.Registry) string {
	var out strings.Builder

	var walk func(cmd *cobra.Command, path string)

	walk = func(cmd *cobra.Command, path string) {
		for _, sub := range cmd.Commands() {
			subPath := strings.TrimSpace(path + " " + sub.Name())
			owner, _ := registry.Owner(sub)

			out.WriteString(subPath + " <- " + owner)

			if registry.Placeholder(sub) {
				out.WriteString(" (placeholder)")
			}

			out.WriteString("\n")
			walk(sub, subPath)
		}
	}
	walk(root, "")

	return out.String()
}

// lookup returns the command at path, command names from root down
// separated by spaces, or nil when there is none.
func lookup(root *cobra.Command, path string) *cobra.Command {
	cmd := root

	for name := range strings.FieldsSeq(path) {
		index := slices.IndexFunc(cmd.Commands(), func(sub *cobra.Command) bool {
			return sub.Name() == name
		})
		if index < 0 {
			return nil
		}

		cmd = cmd.Commands()[index]
	}

	return cmd
}

// at returns a function finding the command at path in a tree, as lookup
// does.
func at(path string) func(root *cobra.Command) *cobra.Command {
	return func(root *cobra.Command) *cobra.Command { return lookup(root, path) }
}

// just returns a function returning cmd whatever the tree.
func just(cmd *cobra.Command) func(root *cobra.Command) *cobra.Command {
	return func(*cobra.Command) *cobra.Command { return cmd }
}

// commands returns cmd and every command under it.
func commands(cmd *cobra.Command) []*cobra.Command {
	all := make([]*cobra.Command, 0, 1+len(cmd.Commands()))

	all = append(all, cmd)

	for _, sub := range cmd.Commands() {
		all = append(all, commands(sub)...)
	}

	return all
}

// owners lists cmd and every command under it with the module the registry
// has it come from, "-" for none, and marks placeholders.
func owners(registry *mount.Registry, cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}

	var out strings.Builder

	for _, each := range commands(cmd) {
		owner, ok := registry.Owner(each)
		if !ok {
			owner = "-"
		}

		out.WriteString(each.Name() + " <- " + owner)

		if registry.Placeholder(each) {
			out.WriteString(" (placeholder)")
		}

		out.WriteString("\n")
	}

	return out.String()
}

// parentOf returns cmd's parent, or nil for no command.
func parentOf(cmd *cobra.Command) *cobra.Command {
	if cmd == nil {
		return nil
	}

	return cmd.Parent()
}

// proxy returns a command like the one tt makes for an external module: it
// parses no flags and fails, keeping the arguments it got in args.
func proxy(name string, args *[]string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, got []string) error {
			*args = got

			return errFailed
		},
	}
}

// TestHang checks where commands land and what is refused.
func TestHang(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		entries func() []mount.Entry
		want    string
		wantErr []string
	}{
		{
			name: "root mount",
			entries: func() []mount.Entry {
				return []mount.Entry{{Module: "a", Path: "", Cmd: leaf("hello")}}
			},
			want: "hello <- a\n",
		},
		{
			name: "placeholder groups",
			entries: func() []mount.Entry {
				return []mount.Entry{{Module: "a", Path: "x  y", Cmd: leaf("z")}}
			},
			want: "x <- a (placeholder)\nx y <- a (placeholder)\nx y z <- a\n",
		},
		{
			name: "provided group is used whatever the order",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "b", Path: "grp", Cmd: leaf("sub")},
					{Module: "a", Path: "", Cmd: group("grp", leaf("own"))},
				}
			},
			want: "grp <- a\ngrp own <- a\ngrp sub <- b\n",
		},
		{
			name: "merge under a legacy group",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "rsop", Path: "replicaset", Cmd: leaf("op1")},
					{
						Module: "builtin", Path: "", Legacy: true,
						Cmd: withAliases(group("replicaset", leaf("status")), "rs"),
					},
				}
			},
			want: "replicaset <- builtin\nreplicaset op1 <- rsop\n" +
				"replicaset status <- builtin\n",
		},
		{
			name: "two modules share a placeholder",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "b", Path: "g", Cmd: leaf("two")},
					{Module: "a", Path: "g", Cmd: leaf("one")},
				}
			},
			want: "g <- a (placeholder)\ng one <- a\ng two <- b\n",
		},
		{
			name: "under its own runnable command",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "a", Path: "run", Cmd: leaf("sub")},
					{Module: "a", Path: "", Cmd: leaf("run")},
				}
			},
			want: "run <- a\nrun sub <- a\n",
		},
		{
			name: "duplicate leaf",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "rsop2", Path: "replicaset", Cmd: leaf("op1")},
					{Module: "rsop1", Path: "replicaset", Cmd: leaf("op1")},
					{Module: "builtin", Path: "", Cmd: group("replicaset"), Legacy: true},
				}
			},
			wantErr: []string{`duplicate command "replicaset op1": modules "rsop1" and "rsop2"`},
		},
		{
			name: "duplicate group",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "b", Path: "", Cmd: group("g", leaf("y"))},
					{Module: "a", Path: "", Cmd: group("g", leaf("x"))},
				}
			},
			wantErr: []string{`duplicate command "g": modules "a" and "b"`},
		},
		{
			name: "duplicate with a command inside a mounted group",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "b", Path: "g", Cmd: leaf("x")},
					{Module: "a", Path: "", Cmd: group("g", leaf("x"))},
				}
			},
			wantErr: []string{`duplicate command "g x": modules "a" and "b"`},
		},
		{
			name: "alias clashes with a name",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "b", Path: "", Cmd: leaf("x")},
					{Module: "a", Path: "", Cmd: leaf("foo", "x")},
				}
			},
			wantErr: []string{`command "foo" of module "a" and command "x" of module "b" ` +
				`both answer to "x"`},
		},
		{
			name: "alias clashes with an alias",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "b", Path: "", Cmd: leaf("bar", "x")},
					{Module: "a", Path: "", Cmd: leaf("foo", "x")},
				}
			},
			wantErr: []string{`command "bar" of module "b" and command "foo" of module "a" ` +
				`both answer to "x"`},
		},
		{
			name: "mount under another module's runnable command",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "b", Path: "run", Cmd: leaf("sub")},
					{Module: "a", Path: "", Cmd: leaf("run")},
				}
			},
			wantErr: []string{`module "b": cannot mount "sub" under "run": it is a command ` +
				`of module "a", not a group`},
		},
		{
			name: "reserved names",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "a", Path: "", Cmd: leaf("help")},
					{Module: "b", Path: "", Cmd: leaf("x", cobra.ShellCompRequestCmd)},
					{Module: "c", Path: "sub", Cmd: leaf("help")},
				}
			},
			wantErr: []string{
				`module "a": command "help": "help" is reserved by tt`,
				`module "b": command "x": "__complete" is reserved by tt`,
			},
		},
		{
			name: "alias as a path segment",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "m", Path: "rs", Cmd: leaf("x")},
					{
						Module: "builtin", Path: "", Legacy: true,
						Cmd: withAliases(group("replicaset"), "rs"),
					},
				}
			},
			wantErr: []string{`module "m": mount path "rs": "rs" is an alias of "replicaset"; ` +
				`a path names commands by their names`},
		},
		{
			name: "invalid entries",
			entries: func() []mount.Entry {
				parented := leaf("child")
				group("parent", parented)

				return []mount.Entry{
					{Module: "a", Path: "x", Cmd: nil},
					{Module: "b", Path: "", Cmd: parented},
					{Module: "", Path: "", Cmd: leaf("anon")},
					{Module: "c", Path: "", Cmd: &cobra.Command{}},
					{Module: "d", Path: "k=v", Cmd: leaf("e")},
				}
			},
			wantErr: []string{
				`module "c": command under "" has no name`,
				`mount of command "anon" under "": no module name`,
				`module "b": command "child" already has a parent "parent"`,
				`module "d": mount path "k=v": "k=v" is not a command name`,
				`module "a": mount under "x" has no command`,
			},
		},
		{
			name: "reserved flags",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "m", Path: "", Cmd: withFlag(leaf("v"), "verbose", "", false)},
					{Module: "m", Path: "", Cmd: withFlag(leaf("w"), "loud", "V", false)},
					{Module: "m", Path: "", Cmd: withFlag(leaf("x"), "dir", "C", true)},
					{Module: "m", Path: "", Cmd: withFlag(leaf("y"), "help", "", false)},
					{Module: "m", Path: "grp", Cmd: withFlag(leaf("z"), "go", "g", false)},
					{
						Module: "builtin", Path: "", Legacy: true,
						Cmd: withFlag(group("grp"), "grp-flag", "g", true),
					},
					// Legacy commands keep flags that clash already.
					{
						Module: "builtin", Path: "", Legacy: true,
						Cmd: withFlag(leaf("create"), "self", "V", false),
					},
				}
			},
			wantErr: []string{
				`module "m": command "v": flag --verbose is reserved by tt`,
				`module "m": command "w": shorthand -V of flag --loud is reserved by tt ` +
					`for --verbose`,
				`module "m": command "x": shorthand -C of flag --dir is reserved by tt ` +
					`for --directory`,
				`module "m": command "y": flag --help is reserved by tt`,
				`module "m": command "grp z": shorthand -g of flag --go is reserved by tt ` +
					`for --grp-flag`,
			},
		},
		{
			name: "shorthand used twice",
			entries: func() []mount.Entry {
				child := withFlag(leaf("child"), "beta", "a", false)

				return []mount.Entry{{
					Module: "m", Path: "",
					Cmd: withFlag(group("g", child), "alpha", "a", true),
				}}
			},
			wantErr: []string{`module "m": command "g child": flags: unable to redefine`},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := newRoot()

			registry, err := mount.Hang(root, testCase.entries(), reserved())
			if testCase.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, testCase.want, render(root, registry))

				return
			}

			require.Error(t, err)
			assert.Nil(t, registry)

			lines := strings.Split(err.Error(), "\n")
			require.Len(t, lines, len(testCase.wantErr), err.Error())

			for i, want := range testCase.wantErr {
				assert.Contains(t, lines[i], want)
			}
		})
	}
}

// TestHangWrapsModuleHooks checks that an error a module's hook returns is
// left to the core to report, and that a legacy command's hooks are left
// alone.
func TestHangWrapsModuleHooks(t *testing.T) {
	t.Parallel()

	failing := func(*cobra.Command, []string) error { return errFailed }
	nothing := func(*cobra.Command, []string) {}

	// Each case runs "x y": x is the mounted group, y the command run.
	for _, testCase := range []struct {
		name   string
		group  cobra.Command
		leaf   cobra.Command
		legacy bool
	}{
		{name: "RunE", leaf: cobra.Command{RunE: failing}},
		{name: "PreRunE", leaf: cobra.Command{Run: nothing, PreRunE: failing}},
		{name: "PostRunE", leaf: cobra.Command{Run: nothing, PostRunE: failing}},
		{name: "PersistentPreRunE", leaf: cobra.Command{Run: nothing, PersistentPreRunE: failing}},
		{
			name: "PersistentPostRunE",
			leaf: cobra.Command{Run: nothing, PersistentPostRunE: failing},
		},
		{
			name:  "PersistentPreRunE of the group",
			group: cobra.Command{PersistentPreRunE: failing},
			leaf:  cobra.Command{Run: nothing},
		},
		{name: "legacy", leaf: cobra.Command{RunE: failing}, legacy: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			parent, child := &testCase.group, &testCase.leaf

			parent.Use, child.Use = "x", "y"
			parent.AddCommand(child)

			root := newRoot()

			_, err := mount.Hang(root, []mount.Entry{
				{Module: "m", Path: "", Cmd: parent, Legacy: testCase.legacy},
			}, reserved())
			require.NoError(t, err)

			var errOut strings.Builder

			root.SetArgs([]string{"x", "y"})
			root.SetOut(&strings.Builder{})
			root.SetErr(&errOut)

			failed, err := root.ExecuteC()
			require.ErrorIs(t, err, errFailed)
			assert.Same(t, child, failed)

			if testCase.legacy {
				assert.False(t, failed.SilenceErrors)
				assert.Contains(t, errOut.String(), "Error: failed")

				return
			}

			assert.True(t, failed.SilenceErrors)
			assert.True(t, failed.SilenceUsage)
			assert.Empty(t, errOut.String(), "cobra must print neither the error nor the usage")
		})
	}
}

// TestHangIsDeterministic checks that the tree and the errors do not depend
// on the order of the entries.
func TestHangIsDeterministic(t *testing.T) {
	t.Parallel()

	replicaset := func() mount.Entry {
		return mount.Entry{
			Module: "builtin", Path: "", Cmd: group("replicaset", leaf("status")), Legacy: true,
		}
	}

	for _, testCase := range []struct {
		name    string
		entries func() []mount.Entry
	}{
		{"tree", func() []mount.Entry {
			return []mount.Entry{
				replicaset(),
				{Module: "builtin", Path: "", Cmd: leaf("version"), Legacy: true},
				{Module: "a", Path: "replicaset", Cmd: leaf("op1")},
				{Module: "b", Path: "replicaset vshard", Cmd: leaf("op2")},
				{Module: "c", Path: "replicaset", Cmd: group("vshard", leaf("own"))},
				{Module: "d", Path: "x y z", Cmd: leaf("deep")},
				{Module: "e", Path: "x", Cmd: leaf("shallow")},
				{Module: "f", Path: "", Cmd: group("x")},
			}
		}},
		{"errors", func() []mount.Entry {
			return []mount.Entry{
				replicaset(),
				{Module: "a", Path: "replicaset", Cmd: leaf("op1")},
				{Module: "b", Path: "replicaset", Cmd: leaf("op1")},
				{Module: "c", Path: "replicaset", Cmd: leaf("op1")},
				{Module: "d", Path: "", Cmd: leaf("run")},
				{Module: "e", Path: "run", Cmd: leaf("sub")},
				{Module: "f", Path: "", Cmd: nil},
				{Module: "g", Path: "", Cmd: leaf("help")},
				{Module: "h", Path: "", Cmd: leaf("v", "version")},
				{Module: "i", Path: "", Cmd: leaf("version")},
			}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			outcome := func(shuffle bool) string {
				entries := testCase.entries()
				if shuffle {
					//nolint:gosec // The order of a test's entries needs no secure random source.
					rand.Shuffle(len(entries), func(i, j int) {
						entries[i], entries[j] = entries[j], entries[i]
					})
				}

				root := newRoot()

				registry, err := mount.Hang(root, entries, reserved())
				if err != nil {
					return "error: " + err.Error()
				}

				return render(root, registry)
			}

			want := outcome(false)
			require.NotEmpty(t, want)

			for range 100 {
				require.Equal(t, want, outcome(true))
			}
		})
	}
}

// TestReplace checks what a replacement leaves in the tree and the registry.
func TestReplace(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		// entries are hung before the replacement.
		entries func() []mount.Entry
		// old returns the command replaced, nil to add cmd.
		old func(root *cobra.Command) *cobra.Command
		// cmd is the command put at the root, a command of module "ext".
		cmd  *cobra.Command
		want string
	}{
		{
			name: "module command",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{Module: "a", Path: "", Cmd: leaf("x")},
					{Module: "b", Path: "", Cmd: leaf("y")},
				}
			},
			old:  at("x"),
			cmd:  leaf("x"),
			want: "x <- ext\ny <- b\n",
		},
		{
			name: "legacy command and what is mounted under it",
			entries: func() []mount.Entry {
				return []mount.Entry{
					{
						Module: "builtin", Path: "", Legacy: true,
						Cmd: withAliases(group("replicaset", leaf("status")), "rs"),
					},
					{Module: "rsop", Path: "replicaset", Cmd: leaf("op1")},
					{Module: "builtin", Path: "", Cmd: leaf("version"), Legacy: true},
				}
			},
			old:  at("replicaset"),
			cmd:  leaf("replicaset", "rs"),
			want: "replicaset <- ext\nversion <- builtin\n",
		},
		{
			name: "placeholder group",
			entries: func() []mount.Entry {
				return []mount.Entry{{Module: "a", Path: "x y", Cmd: leaf("z")}}
			},
			old:  at("x"),
			cmd:  leaf("x"),
			want: "x <- ext\n",
		},
		{
			name: "by a group",
			entries: func() []mount.Entry {
				return []mount.Entry{{Module: "a", Path: "", Cmd: leaf("x")}}
			},
			old:  at("x"),
			cmd:  group("x", leaf("sub")),
			want: "x <- ext\nx sub <- ext\n",
		},
		{
			name: "added",
			entries: func() []mount.Entry {
				return []mount.Entry{{Module: "a", Path: "", Cmd: leaf("x")}}
			},
			old:  just(nil),
			cmd:  leaf("new"),
			want: "new <- ext\nx <- a\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := newRoot()

			registry, err := mount.Hang(root, testCase.entries(), reserved())
			require.NoError(t, err)

			old := testCase.old(root)

			err = registry.Replace(root, old, testCase.cmd, "ext", reserved())
			require.NoError(t, err)
			assert.Equal(t, testCase.want, render(root, registry))

			named := slices.DeleteFunc(slices.Clone(root.Commands()),
				func(sub *cobra.Command) bool { return sub.Name() != testCase.cmd.Name() })
			require.Len(t, named, 1, "one command at the root must have the name")
			assert.Same(t, testCase.cmd, named[0])

			owner, ok := registry.Owner(testCase.cmd)
			assert.True(t, ok)
			assert.Equal(t, "ext", owner)

			if old == nil {
				return
			}

			assert.False(t, old.HasParent())

			for _, gone := range commands(old) {
				_, owned := registry.Owner(gone)
				assert.False(t, owned, "%q must leave the registry", gone.Name())
				assert.False(t, registry.Placeholder(gone), "%q must leave the registry",
					gone.Name())
			}
		})
	}
}

// TestReplaceAliases checks that the aliases of the replaced command leave
// with it: the command put in its place answers to the aliases it has.
func TestReplaceAliases(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		cmd  *cobra.Command
		// found reports whether the alias of the replaced command finds cmd.
		found bool
	}{
		{name: "without the alias", cmd: leaf("replicaset"), found: false},
		{name: "with the alias", cmd: leaf("replicaset", "rs"), found: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := newRoot()

			registry, err := mount.Hang(root, []mount.Entry{{
				Module: "builtin", Path: "", Legacy: true,
				Cmd: withAliases(group("replicaset", leaf("status")), "rs"),
			}}, reserved())
			require.NoError(t, err)

			err = registry.Replace(root, lookup(root, "replicaset"), testCase.cmd, "ext",
				reserved())
			require.NoError(t, err)

			found, _, err := root.Find([]string{"rs"})
			if testCase.found {
				require.NoError(t, err)
				assert.Same(t, testCase.cmd, found)
			} else {
				require.Error(t, err)
				assert.NotSame(t, testCase.cmd, found)
			}
		})
	}
}

// TestReplaceRefuses checks what Replace refuses, and that a refusal leaves
// the tree and the registry as they were.
func TestReplaceRefuses(t *testing.T) {
	t.Parallel()

	// entries are the tree every case starts from.
	entries := func() []mount.Entry {
		return []mount.Entry{
			{Module: "a", Path: "", Cmd: leaf("x")},
			{Module: "b", Path: "", Cmd: leaf("y", "yy")},
			{Module: "c", Path: "", Cmd: group("g", leaf("sub"))},
		}
	}

	for _, testCase := range []struct {
		name string
		// old returns the command to replace, nil to add cmd.
		old func(root *cobra.Command) *cobra.Command
		// cmd returns the command to put at the root.
		cmd     func(root *cobra.Command) *cobra.Command
		module  string
		wantErr string
	}{
		{
			name: "reserved name", old: just(nil), cmd: just(leaf("help")), module: "ext",
			wantErr: `module "ext": command "help": "help" is reserved by tt`,
		},
		{
			name: "reserved alias", old: at("x"), module: "ext",
			cmd:     just(leaf("x", cobra.ShellCompRequestCmd)),
			wantErr: `module "ext": command "x": "__complete" is reserved by tt`,
		},
		{
			name: "name of another command's alias", old: just(nil), cmd: just(leaf("yy")),
			module: "ext",
			wantErr: `command "y" of module "b" and command "yy" of module "ext" ` +
				`both answer to "yy"`,
		},
		{
			name: "alias of another command's name", old: at("x"), cmd: just(leaf("x", "y")),
			module: "ext",
			wantErr: `command "y" of module "b" and command "x" of module "ext" ` +
				`both answer to "y"`,
		},
		{
			name: "alias of another command's alias", old: at("x"), cmd: just(leaf("x", "yy")),
			module: "ext",
			wantErr: `command "y" of module "b" and command "x" of module "ext" ` +
				`both answer to "yy"`,
		},
		{
			name: "duplicate added", old: just(nil), cmd: just(leaf("x")), module: "ext",
			wantErr: `duplicate command "x": modules "a" and "ext"`,
		},
		{
			name: "names differ", old: at("x"), cmd: just(leaf("z")), module: "ext",
			wantErr: `module "ext": command "z" cannot replace "x": the names differ`,
		},
		{
			name: "old under a group", old: at("g sub"), cmd: just(leaf("sub")), module: "ext",
			wantErr: `module "ext": command "sub" cannot replace "tt g sub": ` +
				`it is not at the root`,
		},
		{
			name: "old outside the tree", old: just(leaf("x")), cmd: just(leaf("x")),
			module:  "ext",
			wantErr: `module "ext": command "x" cannot replace "x": it is not at the root`,
		},
		{
			name: "old is the root", old: at(""), cmd: just(leaf("tt")), module: "ext",
			wantErr: `module "ext": command "tt" cannot replace "tt": it is not at the root`,
		},
		{
			name: "no command", old: at("x"), cmd: just(nil), module: "ext",
			wantErr: `module "ext": mount under "" has no command`,
		},
		{
			name: "unnamed command", old: at("x"), cmd: just(&cobra.Command{}), module: "ext",
			wantErr: `module "ext": command under "" has no name`,
		},
		{
			name: "command with a parent", old: at("x"), module: "ext",
			cmd: func(*cobra.Command) *cobra.Command {
				child := leaf("x")
				group("other", child)

				return child
			},
			wantErr: `module "ext": command "x" already has a parent "other"`,
		},
		{
			name: "old itself", old: at("x"), cmd: at("x"), module: "ext",
			wantErr: `module "ext": command "x" already has a parent "tt"`,
		},
		{
			name: "no module", old: at("x"), cmd: just(leaf("x")), module: "",
			wantErr: `mount of command "x" under "": no module name`,
		},
		{
			name: "reserved flag", old: at("x"), module: "ext",
			cmd:     just(withFlag(leaf("x"), "verbose", "", false)),
			wantErr: `module "ext": command "x": flag --verbose is reserved by tt`,
		},
		{
			name: "reserved shorthand under the command", old: at("x"), module: "ext",
			cmd: just(group("x", withFlag(leaf("sub"), "dir", "C", false))),
			wantErr: `module "ext": command "x sub": shorthand -C of flag --dir is reserved ` +
				`by tt for --directory`,
		},
		{
			name: "shorthand used twice", old: at("x"), module: "ext",
			cmd: just(withFlag(group("x", withFlag(leaf("child"), "beta", "a", false)),
				"alpha", "a", true)),
			wantErr: `module "ext": command "x child": flags: unable to redefine`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := newRoot()

			registry, err := mount.Hang(root, entries(), reserved())
			require.NoError(t, err)

			old, cmd := testCase.old(root), testCase.cmd(root)
			oldParent, cmdParent := parentOf(old), parentOf(cmd)
			tree, oldOwners, cmdOwners := render(root, registry), owners(registry, old),
				owners(registry, cmd)

			err = registry.Replace(root, old, cmd, testCase.module, reserved())
			require.ErrorContains(t, err, testCase.wantErr)

			assert.Equal(t, tree, render(root, registry), "the tree must be as it was")
			assert.Equal(t, oldOwners, owners(registry, old), "the registry must be as it was")
			assert.Equal(t, cmdOwners, owners(registry, cmd), "the registry must be as it was")
			assert.Same(t, oldParent, parentOf(old))
			assert.Same(t, cmdParent, parentOf(cmd))
		})
	}
}

// TestReplaceWrapsHooks checks that an error of what Replace puts in the
// tree is left to the core to report, as a module command's is.
func TestReplaceWrapsHooks(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		// old returns the command replaced, nil to add cmd.
		old func(root *cobra.Command) *cobra.Command
		// cmd returns the command put at the root, with a proxy in it that
		// keeps its arguments in args.
		cmd func(args *[]string) *cobra.Command
		// path is where the proxy is, the command run.
		path string
	}{
		{
			name: "in place of a legacy command",
			old:  at("x"),
			cmd:  func(args *[]string) *cobra.Command { return proxy("x", args) },
			path: "x",
		},
		{
			name: "added",
			old:  just(nil),
			cmd:  func(args *[]string) *cobra.Command { return proxy("new", args) },
			path: "new",
		},
		{
			name: "under the command put in place",
			old:  at("x"),
			cmd:  func(args *[]string) *cobra.Command { return group("x", proxy("y", args)) },
			path: "x y",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := newRoot()

			registry, err := mount.Hang(root, []mount.Entry{
				{Module: "builtin", Path: "", Cmd: leaf("x"), Legacy: true},
			}, reserved())
			require.NoError(t, err)

			var args []string

			err = registry.Replace(root, testCase.old(root), testCase.cmd(&args), "ext",
				reserved())
			require.NoError(t, err)

			var out, errOut strings.Builder

			root.SetArgs(append(strings.Fields(testCase.path), "--any", "value"))
			root.SetOut(&out)
			root.SetErr(&errOut)

			failed, err := root.ExecuteC()
			require.ErrorIs(t, err, errFailed)
			assert.Same(t, lookup(root, testCase.path), failed)
			assert.Equal(t, []string{"--any", "value"}, args, "the proxy parses no flags")
			assert.True(t, failed.SilenceErrors)
			assert.True(t, failed.SilenceUsage)
			assert.Empty(t, errOut.String(), "cobra must print neither the error nor the usage")
			assert.Empty(t, out.String(), "cobra must print neither the error nor the usage")
		})
	}
}
