package mount_test

import (
	"errors"
	"math/rand/v2"
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

// TestHang checks where commands land and what is refused.
func TestHang(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := newRoot()

			registry, err := mount.Hang(root, tc.entries(), reserved())
			if tc.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, tc.want, render(root, registry))

				return
			}

			require.Error(t, err)
			assert.Nil(t, registry)

			lines := strings.Split(err.Error(), "\n")
			require.Len(t, lines, len(tc.wantErr), err.Error())

			for i, want := range tc.wantErr {
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
	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parent, child := &tc.group, &tc.leaf

			parent.Use, child.Use = "x", "y"
			parent.AddCommand(child)

			root := newRoot()

			_, err := mount.Hang(root, []mount.Entry{
				{Module: "m", Path: "", Cmd: parent, Legacy: tc.legacy},
			}, reserved())
			require.NoError(t, err)

			var errOut strings.Builder

			root.SetArgs([]string{"x", "y"})
			root.SetOut(&strings.Builder{})
			root.SetErr(&errOut)

			failed, err := root.ExecuteC()
			require.ErrorIs(t, err, errFailed)
			assert.Same(t, child, failed)

			if tc.legacy {
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

	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			outcome := func(shuffle bool) string {
				entries := tc.entries()
				if shuffle {
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
