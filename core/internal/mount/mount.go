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
//
// Once hung, the tree can still change at the root: Replace puts a module's
// command in place of a command there, or adds it there, checked and
// prepared as Hang would. It is what lets a command found only after the
// modules are hung, such as an external module, take over a top-level
// command.
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
// mount created it. It reports false for a command neither Hang nor Replace
// hung, and for one Replace took out of the tree.
func (r *Registry) Owner(cmd *cobra.Command) (string, bool) {
	owner, ok := r.owners[cmd]

	return owner, ok
}

// Placeholder reports whether cmd is a group in the tree that Hang created
// because no module provided it.
func (r *Registry) Placeholder(cmd *cobra.Command) bool {
	return r.placeholders[cmd]
}

// The errors Hang and Replace refuse a command with. Each is the reason part
// of a message that names the module, the command and the path involved.
var (
	errNoModuleName      = errors.New("no module name")
	errNoCommand         = errors.New("has no command")
	errNoCommandName     = errors.New("has no name")
	errHasParent         = errors.New("already has a parent")
	errNotCommandName    = errors.New("is not a command name")
	errAliasInPath       = errors.New("a path names commands by their names")
	errNotGroup          = errors.New("not a group")
	errReservedName      = errors.New("reserved by tt")
	errDuplicateCommand  = errors.New("duplicate command")
	errAmbiguousName     = errors.New("both answer to")
	errFlagsNotMergeable = errors.New("flags")
	errNotAtRoot         = errors.New("not at the root")
	errNameMismatch      = errors.New("the names differ")
)

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
		err := validate(entry)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		err = hang(root, mount{Entry: entry, segments: strings.Fields(entry.Path)},
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

// Replace hangs cmd, a command of module, at the root in place of old, a
// command at the root, or adds cmd at the root when old is nil. old and its
// subtree leave the tree and the registry; cmd and its subtree are recorded
// as module's and prepared like the commands of any module. old's aliases
// leave with it: cmd answers to its own name and aliases only, so a caller
// that means cmd to answer to old's aliases gives them to cmd.
//
// cmd is refused where Hang would refuse it mounted at the root with old
// gone, and so is an old that is not at the root or is named other than
// cmd. A refusal leaves the tree and the registry as they were.
func (r *Registry) Replace(root, old, cmd *cobra.Command, module string, reserved Reserved) error {
	entry := Entry{Module: module, Path: "", Cmd: cmd, Legacy: false}

	err := validate(entry)
	if err != nil {
		return err
	}

	mnt := mount{Entry: entry, segments: nil}

	err = checkReplaced(root, old, mnt)
	if err != nil {
		return err
	}

	err = checkNames(root, root, mnt, r, reserved, old)
	if err != nil {
		return err
	}

	// The flags are checked and merged with cmd in the tree, beside old, the
	// way cobra merges them when it runs cmd; a refused cmd comes out again.
	root.AddCommand(cmd)

	err = prepare(root, mnt, reserved)
	if err != nil {
		root.RemoveCommand(cmd)

		return err
	}

	if old != nil {
		root.RemoveCommand(old)
		r.forget(old)
	}

	r.record(cmd, module)

	return nil
}

// record records top and every command under it as module's.
func (r *Registry) record(top *cobra.Command, module string) {
	for _, cmd := range subtree(top) {
		r.owners[cmd] = module
	}
}

// forget drops top and every command under it from the registry.
func (r *Registry) forget(top *cobra.Command) {
	for _, cmd := range subtree(top) {
		delete(r.owners, cmd)
		delete(r.placeholders, cmd)
	}
}

// compareEntries orders entries by the depth of their path, then the path,
// the command name and the module: a group is hung before whatever is
// mounted under it, and the result does not depend on the input order.
func compareEntries(left, right Entry) int {
	pathLeft, pathRight := strings.Fields(left.Path), strings.Fields(right.Path)

	return cmp.Or(
		cmp.Compare(len(pathLeft), len(pathRight)),
		slices.Compare(pathLeft, pathRight),
		cmp.Compare(commandName(left.Cmd), commandName(right.Cmd)),
		cmp.Compare(left.Module, right.Module),
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
		return fmt.Errorf("mount of command %q under %q: %w",
			commandName(entry.Cmd), entry.Path, errNoModuleName)
	}

	switch {
	case entry.Cmd == nil:
		return fmt.Errorf("module %q: mount under %q %w", entry.Module, entry.Path, errNoCommand)
	case entry.Cmd.Name() == "":
		return fmt.Errorf("module %q: command under %q %w",
			entry.Module, entry.Path, errNoCommandName)
	case entry.Cmd.HasParent():
		return fmt.Errorf("module %q: command %q %w %q",
			entry.Module, entry.Cmd.Name(), errHasParent, entry.Cmd.Parent().CommandPath())
	}

	for segment := range strings.FieldsSeq(entry.Path) {
		if strings.Contains(segment, "=") {
			return fmt.Errorf("module %q: mount path %q: %q %w",
				entry.Module, entry.Path, segment, errNotCommandName)
		}
	}

	return nil
}

// checkReplaced refuses to put mnt's command in place of old unless old is a
// command at the root with the same name. A nil old replaces nothing.
func checkReplaced(root, old *cobra.Command, mnt mount) error {
	switch {
	case old == nil:
		return nil
	case old.Parent() != root:
		return fmt.Errorf("module %q: command %q cannot replace %q: it is %w",
			mnt.Module, mnt.Cmd.Name(), old.CommandPath(), errNotAtRoot)
	case old.Name() != mnt.Cmd.Name():
		return fmt.Errorf("module %q: command %q cannot replace %q: %w",
			mnt.Module, mnt.Cmd.Name(), old.Name(), errNameMismatch)
	}

	return nil
}

// hang adds one mount's command to the tree.
func hang(root *cobra.Command, mnt mount, registry *Registry, reserved Reserved) error {
	parent, err := walk(root, mnt, registry)
	if err != nil {
		return err
	}

	err = checkNames(root, parent, mnt, registry, reserved, nil)
	if err != nil {
		return err
	}

	parent.AddCommand(mnt.Cmd)
	registry.record(mnt.Cmd, mnt.Module)

	if mnt.Legacy {
		return nil
	}

	return prepare(root, mnt, reserved)
}

// prepare readies the commands of mnt, in the tree under root, for the core:
// it refuses the flags tt reserves and the ones cobra cannot merge, and
// wraps the hooks.
func prepare(root *cobra.Command, mnt mount, reserved Reserved) error {
	err := checkFlags(root, mnt, reserved)
	if err != nil {
		return err
	}

	err = mergeFlags(mnt)
	if err != nil {
		return err
	}

	for _, cmd := range subtree(mnt.Cmd) {
		wrapHooks(cmd)
	}

	return nil
}

// walk follows mnt's path from root and returns the command to hang mnt's
// command under, creating the groups on the path that do not exist.
func walk(root *cobra.Command, mnt mount, registry *Registry) (*cobra.Command, error) {
	parent := root

	for _, segment := range mnt.segments {
		child := byName(parent, segment)

		if child == nil {
			if aliased := byAlias(parent, segment); aliased != nil {
				return nil, fmt.Errorf("module %q: mount path %q: %q is an alias of %q; %w",
					mnt.Module, mnt.Path, segment, displayPath(aliased), errAliasInPath)
			}

			child = &cobra.Command{Use: segment}
			parent.AddCommand(child)

			registry.owners[child] = mnt.Module
			registry.placeholders[child] = true
		} else if owner := ownerName(registry, child); child.Runnable() && owner != mnt.Module {
			return nil, fmt.Errorf("module %q: cannot mount %q under %q: it is a command "+
				"of module %q, %w", mnt.Module, mnt.Cmd.Name(), displayPath(child), owner,
				errNotGroup)
		}

		parent = child
	}

	return parent, nil
}

// checkNames refuses a command whose name or alias is taken next to it by a
// command other than except, or reserved at the root.
func checkNames(
	root, parent *cobra.Command, mnt mount, registry *Registry, reserved Reserved,
	except *cobra.Command,
) error {
	names := append([]string{mnt.Cmd.Name()}, mnt.Cmd.Aliases...)
	path := strings.Join(append(slices.Clone(mnt.segments), mnt.Cmd.Name()), " ")

	if parent == root {
		for _, name := range names {
			if slices.Contains(reserved.Names, name) {
				return fmt.Errorf("module %q: command %q: %q is %w",
					mnt.Module, path, name, errReservedName)
			}
		}
	}

	for _, sibling := range parent.Commands() {
		if sibling == except {
			continue
		}

		siblingNames := append([]string{sibling.Name()}, sibling.Aliases...)

		for _, name := range names {
			if !slices.Contains(siblingNames, name) {
				continue
			}

			if sibling.Name() == mnt.Cmd.Name() {
				return fmt.Errorf("%w %q: modules %q and %q",
					errDuplicateCommand, path, ownerName(registry, sibling), mnt.Module)
			}

			return fmt.Errorf("command %q of module %q and command %q of module %q "+
				"%w %q", displayPath(sibling), ownerName(registry, sibling),
				path, mnt.Module, errAmbiguousName, name)
		}
	}

	return nil
}

// checkFlags refuses a flag of mnt's commands that tt reserves: one of
// reserved.Flags, a flag of the root, or a persistent flag of a group mnt is
// mounted under.
func checkFlags(root *cobra.Command, mnt mount, reserved Reserved) error {
	taken := []*pflag.FlagSet{root.Flags(), root.PersistentFlags()}
	if reserved.Flags != nil {
		taken = append(taken, reserved.Flags)
	}

	for above := mnt.Cmd.Parent(); above != nil && above != root; above = above.Parent() {
		taken = append(taken, above.PersistentFlags())
	}

	var errs []error

	for _, cmd := range subtree(mnt.Cmd) {
		visit := func(flag *pflag.Flag) {
			err := checkFlag(taken, flag)
			if err != nil {
				errs = append(errs, fmt.Errorf("module %q: command %q: %w",
					mnt.Module, displayPath(cmd), err))
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

// mergeFlags merges the inherited flags into every command of mnt the way
// cobra does when it runs one, and turns the panic cobra's flag library
// raises for a shorthand used twice into an error.
func mergeFlags(mnt mount) error {
	var errs []error

	for _, cmd := range subtree(mnt.Cmd) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					errs = append(errs, fmt.Errorf("module %q: command %q: %w: %v",
						mnt.Module, displayPath(cmd), errFlagsNotMergeable, r))
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
	commands := make([]*cobra.Command, 0, 1+len(cmd.Commands()))

	commands = append(commands, cmd)

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
