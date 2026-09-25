// Package mount hangs the commands modules contribute on tt's root command.
//
// Every command lands where its mount says, whatever order the modules
// were listed in: mounts are hung shallowest first, so the group a module
// provides is in place before the commands other modules mount under it,
// and a group nobody provides is created as a placeholder. A group that is
// not itself a command is shared: any module may mount under it. What the
// tree cannot hold - two commands answering to one name, a command under
// another module's runnable command, a flag tt reserves - is refused with an
// error naming the modules involved.
//
// The commands of a module are also prepared for the core: their hooks are
// wrapped so that the error they return is reported by the core, once, and
// not by cobra. Legacy commands - the ones tt built before modules - are
// hung as they are.
package mount

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Entry is one command a module mounts.
type Entry struct {
	// Module is the name of the module the command comes from.
	Module string
	// Path names the command's parent by command names from the root down,
	// separated by spaces; "" is the root.
	Path string
	// Cmd is the command, with no parent.
	Cmd *cobra.Command
	// Legacy marks a command tt built before modules. It is hung without the
	// checks and the hook wrapping a module command gets.
	Legacy bool
}

// Reserved is what no module may take.
type Reserved struct {
	// Names are the command names no command may have, or have as an alias,
	// at the root.
	Names []string
	// Flags are the flags no module command may declare, by name or by
	// shorthand. The flags of the root and the persistent flags of the
	// groups a command is mounted under are reserved as well.
	Flags *pflag.FlagSet
}

// Registry records which module each command in the tree comes from.
type Registry struct {
	owners       map[*cobra.Command]string
	placeholders map[*cobra.Command]bool
}

// Owner returns the module cmd comes from: the module that mounted it or
// the command it is under, and for a placeholder group the module whose
// mount created it. It reports false for a command Hang did not hang.
func (r *Registry) Owner(cmd *cobra.Command) (string, bool) {
	owner, ok := r.owners[cmd]

	return owner, ok
}

// Placeholder reports whether cmd is a group Hang created because no module
// provided it.
func (r *Registry) Placeholder(cmd *cobra.Command) bool {
	return r.placeholders[cmd]
}

// mount is a valid Entry with its path split into command names.
type mount struct {
	Entry

	segments []string
}

// Hang adds the commands of entries to root and returns who owns what. It
// refuses what the tree cannot hold and returns every such problem, joined;
// the tree is then incomplete and must not be run.
func Hang(root *cobra.Command, entries []Entry, reserved Reserved) (*Registry, error) {
	registry := &Registry{
		owners:       map[*cobra.Command]string{},
		placeholders: map[*cobra.Command]bool{},
	}

	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, compareEntries)

	var errs []error

	for _, entry := range sorted {
		if err := validate(entry); err != nil {
			errs = append(errs, err)

			continue
		}

		err := hang(root, mount{Entry: entry, segments: strings.Fields(entry.Path)},
			registry, reserved)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) != 0 {
		return nil, errors.Join(errs...)
	}

	return registry, nil
}

// compareEntries orders entries by the depth of their path, then the path,
// the command name and the module: a group is hung before whatever is
// mounted under it, and the result does not depend on the input order.
func compareEntries(a, b Entry) int {
	pathA, pathB := strings.Fields(a.Path), strings.Fields(b.Path)

	return cmp.Or(
		cmp.Compare(len(pathA), len(pathB)),
		slices.Compare(pathA, pathB),
		cmp.Compare(commandName(a.Cmd), commandName(b.Cmd)),
		cmp.Compare(a.Module, b.Module),
	)
}

// commandName returns cmd's name, or "" for no command.
func commandName(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}

	return cmd.Name()
}

// validate checks what an entry must be before it can be hung.
func validate(entry Entry) error {
	if entry.Module == "" {
		return fmt.Errorf("mount of command %q under %q: no module name",
			commandName(entry.Cmd), entry.Path)
	}

	switch {
	case entry.Cmd == nil:
		return fmt.Errorf("module %q: mount under %q has no command", entry.Module, entry.Path)
	case entry.Cmd.Name() == "":
		return fmt.Errorf("module %q: command under %q has no name", entry.Module, entry.Path)
	case entry.Cmd.HasParent():
		return fmt.Errorf("module %q: command %q already has a parent %q",
			entry.Module, entry.Cmd.Name(), entry.Cmd.Parent().CommandPath())
	}

	for segment := range strings.FieldsSeq(entry.Path) {
		if strings.Contains(segment, "=") {
			return fmt.Errorf("module %q: mount path %q: %q is not a command name",
				entry.Module, entry.Path, segment)
		}
	}

	return nil
}

// hang adds one mount's command to the tree.
func hang(root *cobra.Command, m mount, registry *Registry, reserved Reserved) error {
	parent, err := walk(root, m, registry)
	if err != nil {
		return err
	}

	if err := checkNames(root, parent, m, registry, reserved); err != nil {
		return err
	}

	parent.AddCommand(m.Cmd)

	for _, cmd := range subtree(m.Cmd) {
		registry.owners[cmd] = m.Module
	}

	if m.Legacy {
		return nil
	}

	if err := checkFlags(root, m, reserved); err != nil {
		return err
	}

	if err := mergeFlags(m); err != nil {
		return err
	}

	for _, cmd := range subtree(m.Cmd) {
		wrapHooks(cmd)
	}

	return nil
}

// walk follows m's path from root and returns the command to hang m's
// command under, creating the groups on the path that do not exist.
func walk(root *cobra.Command, m mount, registry *Registry) (*cobra.Command, error) {
	parent := root

	for _, segment := range m.segments {
		child := byName(parent, segment)

		if child == nil {
			if aliased := byAlias(parent, segment); aliased != nil {
				return nil, fmt.Errorf("module %q: mount path %q: %q is an alias of %q; "+
					"a path names commands by their names",
					m.Module, m.Path, segment, displayPath(aliased))
			}

			child = &cobra.Command{Use: segment}
			parent.AddCommand(child)

			registry.owners[child] = m.Module
			registry.placeholders[child] = true
		} else if owner := ownerName(registry, child); child.Runnable() && owner != m.Module {
			return nil, fmt.Errorf("module %q: cannot mount %q under %q: it is a command "+
				"of module %q, not a group", m.Module, m.Cmd.Name(), displayPath(child), owner)
		}

		parent = child
	}

	return parent, nil
}

// checkNames refuses a command whose name or alias is taken next to it, or
// reserved at the root.
func checkNames(
	root, parent *cobra.Command, m mount, registry *Registry, reserved Reserved,
) error {
	names := append([]string{m.Cmd.Name()}, m.Cmd.Aliases...)
	path := strings.Join(append(slices.Clone(m.segments), m.Cmd.Name()), " ")

	if parent == root {
		for _, name := range names {
			if slices.Contains(reserved.Names, name) {
				return fmt.Errorf("module %q: command %q: %q is reserved by tt",
					m.Module, path, name)
			}
		}
	}

	for _, sibling := range parent.Commands() {
		siblingNames := append([]string{sibling.Name()}, sibling.Aliases...)

		for _, name := range names {
			if !slices.Contains(siblingNames, name) {
				continue
			}

			if sibling.Name() == m.Cmd.Name() {
				return fmt.Errorf("duplicate command %q: modules %q and %q",
					path, ownerName(registry, sibling), m.Module)
			}

			return fmt.Errorf("command %q of module %q and command %q of module %q "+
				"both answer to %q", displayPath(sibling), ownerName(registry, sibling),
				path, m.Module, name)
		}
	}

	return nil
}

// checkFlags refuses a flag of m's commands that tt reserves: one of
// reserved.Flags, a flag of the root, or a persistent flag of a group m is
// mounted under.
func checkFlags(root *cobra.Command, m mount, reserved Reserved) error {
	taken := []*pflag.FlagSet{root.Flags(), root.PersistentFlags()}
	if reserved.Flags != nil {
		taken = append(taken, reserved.Flags)
	}

	for above := m.Cmd.Parent(); above != nil && above != root; above = above.Parent() {
		taken = append(taken, above.PersistentFlags())
	}

	var errs []error

	for _, cmd := range subtree(m.Cmd) {
		visit := func(flag *pflag.Flag) {
			if err := checkFlag(taken, flag); err != nil {
				errs = append(errs, fmt.Errorf("module %q: command %q: %w",
					m.Module, displayPath(cmd), err))
			}
		}

		cmd.PersistentFlags().VisitAll(visit)
		cmd.Flags().VisitAll(visit)
	}

	return errors.Join(errs...)
}

// errReservedFlag reports a module flag tt reserves.
var errReservedFlag = errors.New("reserved by tt")

// checkFlag refuses flag when a flag set in taken has its name or
// shorthand.
func checkFlag(taken []*pflag.FlagSet, flag *pflag.Flag) error {
	for _, set := range taken {
		if set.Lookup(flag.Name) != nil {
			return fmt.Errorf("flag --%s is %w", flag.Name, errReservedFlag)
		}

		if flag.Shorthand == "" {
			continue
		}

		if other := set.ShorthandLookup(flag.Shorthand); other != nil {
			return fmt.Errorf("shorthand -%s of flag --%s is %w for --%s",
				flag.Shorthand, flag.Name, errReservedFlag, other.Name)
		}
	}

	return nil
}

// mergeFlags merges the inherited flags into every command of m the way
// cobra does when it runs one, and turns the panic cobra's flag library
// raises for a shorthand used twice into an error.
func mergeFlags(m mount) error {
	var errs []error

	for _, cmd := range subtree(m.Cmd) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					errs = append(errs, fmt.Errorf("module %q: command %q: flags: %v",
						m.Module, displayPath(cmd), r))
				}
			}()

			_ = cmd.InheritedFlags()
			_ = cmd.LocalFlags()
		}()
	}

	return errors.Join(errs...)
}

// hook is the signature of cobra's error-returning hooks.
type hook = func(*cobra.Command, []string) error

// wrapHooks makes cmd's error-returning hooks hand their error to the core:
// cobra is told to print neither the error nor the usage, since the core
// reports the error itself and prints the usage only for a usage error.
func wrapHooks(cmd *cobra.Command) {
	for _, target := range []*hook{
		&cmd.PersistentPreRunE, &cmd.PreRunE, &cmd.RunE, &cmd.PostRunE, &cmd.PersistentPostRunE,
	} {
		if *target != nil {
			*target = silenced(*target)
		}
	}
}

// silenced returns run with cobra's error and usage printing turned off for
// the command that failed.
func silenced(run hook) hook {
	return func(cmd *cobra.Command, args []string) error {
		err := run(cmd, args)
		if err != nil {
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
		}

		return err
	}
}

// byName returns the subcommand of parent named name, or nil.
func byName(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}

	return nil
}

// byAlias returns the subcommand of parent with the alias name, or nil.
func byAlias(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if slices.Contains(sub.Aliases, name) {
			return sub
		}
	}

	return nil
}

// subtree returns cmd and every command under it, parents first.
func subtree(cmd *cobra.Command) []*cobra.Command {
	commands := []*cobra.Command{cmd}

	for _, sub := range cmd.Commands() {
		commands = append(commands, subtree(sub)...)
	}

	return commands
}

// ownerName names the module cmd comes from, or "tt" for a command no
// module mounted.
func ownerName(registry *Registry, cmd *cobra.Command) string {
	if owner, ok := registry.Owner(cmd); ok {
		return owner
	}

	return "tt"
}

// displayPath returns cmd's path without the root's name: "replicaset
// vshard".
func displayPath(cmd *cobra.Command) string {
	var names []string

	for c := cmd; c.HasParent(); c = c.Parent() {
		names = append([]string{c.Name()}, names...)
	}

	return strings.Join(names, " ")
}
