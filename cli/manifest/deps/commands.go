package deps

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
)

// Add declares name in [dependencies] — or, with dev, in [dev_dependencies] —
// at the given constraint, and re-resolves the lock around it.
//
// An empty constraint means defaultConstraint ("*"): "whatever the registry
// serves". A name the table already declares has its constraint rewritten and
// the previous one reported in Result.Change; nothing else about the
// declaration moves, so a long-form entry keeps its source, registry and
// comments.
//
// Every rock the lock already holds is pinned, so only the added rock and
// whatever it drags in behind it can move. The added rock is not in the lock,
// so it resolves fresh.
func Add(
	ctx context.Context, opts Options, name, constraint string, dev bool,
) (*Result, error) {
	engine, err := engineFor(opts)
	if err != nil {
		return nil, err
	}

	return addWith(ctx, opts, engine, name, constraint, dev)
}

// Declare is Add as tt package add runs it, which differs in one case: with
// no constraint, a name the table declares already keeps the declaration it
// has, as cargo add keeps the version requirement of a dependency it is
// given again without one. Only an explicit constraint rewrites a
// declaration. Result.Kept reports the case, and Result.Change carries the
// constraint kept.
//
// A kept declaration edits nothing. The lock is resolved only when it needs
// it: a project with no lock, or a lock stale for the manifest, is resolved
// as Resolve resolves it, every version it holds kept; a lock that is
// current is left as it is, and the run is a no-op.
func Declare(
	ctx context.Context, opts Options, name, constraint string, dev bool,
) (*Result, error) {
	engine, err := engineFor(opts)
	if err != nil {
		return nil, err
	}

	return declareWith(ctx, opts, engine, name, constraint, dev)
}

// declareWith is Declare against an injected resolver.
func declareWith(
	ctx context.Context, opts Options, res resolver, name, constraint string, dev bool,
) (*Result, error) {
	return addOrDeclare(ctx, opts, res, name, constraint, dev, true)
}

// addWith is Add against an injected resolver.
func addWith(
	ctx context.Context, opts Options, res resolver, name, constraint string, dev bool,
) (*Result, error) {
	return addOrDeclare(ctx, opts, res, name, constraint, dev, false)
}

// addOrDeclare is Add, or with keep Declare, against an injected resolver.
func addOrDeclare(
	ctx context.Context, opts Options, res resolver,
	name, constraint string, dev, keep bool,
) (*Result, error) {
	ctx, release, err := lockProject(ctx, opts)
	if err != nil {
		return nil, err
	}

	defer release()

	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	table := manifest.TableDependencies
	if dev {
		table = manifest.TableDevDependencies
	}

	editor, err := manifest.NewEditor(proj.source)
	if err != nil {
		return nil, stateErrorf("reading %s for editing: %w", manifestFileName, err)
	}

	if keep && constraint == "" && slices.Contains(editor.Locate(name), table) {
		warnOtherTable(opts, editor, table, name)

		return keepDeclaration(ctx, opts, res, proj, table, name)
	}

	if constraint == "" {
		constraint = defaultConstraint
	}

	constraintErr := resolve.ValidateConstraint(constraint)
	if constraintErr != nil {
		return nil, stateErrorf("%w", constraintErr)
	}

	warnOtherTable(opts, editor, table, name)

	change, err := editor.SetDependency(table, name, constraint)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	// Hold every version the lock already chose: an edit to one dependency is
	// not a request to move the others.
	return apply(ctx, opts, res, proj, editor.Bytes(), resolve.PinsFromLock(proj.lock), change)
}

// keepDeclaration is the run of Declare for a name table declares already
// and no constraint: the declaration is kept, and the lock is resolved, as
// Resolve resolves it, only when there is none or it is stale.
func keepDeclaration(
	ctx context.Context, opts Options, res resolver,
	proj *project, table manifest.DepTable, name string,
) (*Result, error) {
	declaration := proj.manifest.Dependencies[name]
	if table == manifest.TableDevDependencies {
		declaration = proj.manifest.DevDependencies[name]
	}

	change := manifest.Change{
		Existed:          true,
		Previous:         declaration.Version,
		PreviousRegistry: declaration.Registry,
	}

	if proj.lock != nil {
		//nolint:contextcheck // The check reads files and asks git, as a build's does.
		stale, _, err := resolve.IsStale(opts.ProjectDir, proj.manifest, proj.lock)
		if err == nil && !stale {
			return &Result{
				Manifest: proj.manifest,
				Lock:     proj.lock,
				Moves:    []Move{},
				Rewrites: nil,
				Change:   change,
				Kept:     true,
			}, nil
		}
	}

	result, err := apply(ctx, opts, res, proj, nil, resolve.PinsFromLock(proj.lock), change)
	if err != nil {
		return nil, err
	}

	result.Kept = true

	return result, nil
}

// AddFromRegistry declares name in [dependencies] - or, with dev, in
// [dev_dependencies] - in the long form, at the constraint and with the
// registry the rock is to come from, then re-resolves the lock as Add does. A
// declaration already there has its version and registry set to those given,
// the rest of it kept (manifest.Editor.AddRegistryDependency), and
// Result.Change reports what they were; one that says both already is not
// rewritten, and the lock is resolved against it.
func AddFromRegistry(
	ctx context.Context, opts Options, name, constraint, registry string, dev bool,
) (*Result, error) {
	engine, err := engineFor(opts)
	if err != nil {
		return nil, err
	}

	return addFromRegistryWith(ctx, opts, engine, name, constraint, registry, dev)
}

// addFromRegistryWith is AddFromRegistry against an injected resolver.
func addFromRegistryWith(
	ctx context.Context, opts Options, res resolver,
	name, constraint, registry string, dev bool,
) (*Result, error) {
	ctx, release, err := lockProject(ctx, opts)
	if err != nil {
		return nil, err
	}

	defer release()

	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	editor, err := manifest.NewEditor(proj.source)
	if err != nil {
		return nil, stateErrorf("reading %s for editing: %w", manifestFileName, err)
	}

	table := manifest.TableDependencies
	if dev {
		table = manifest.TableDevDependencies
	}

	warnOtherTable(opts, editor, table, name)

	change, err := editor.AddRegistryDependency(table, name, constraint, registry)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	var edited []byte

	if !bytes.Equal(editor.Bytes(), proj.source) {
		edited = editor.Bytes()
	}

	return apply(ctx, opts, res, proj, edited, resolve.PinsFromLock(proj.lock), change)
}

// warnOtherTable reports a name that is also declared in the table the add is
// not targeting. Declaring a rock as both a runtime and a dev dependency is
// legal TOML and parses, but it is almost always a mistake, and the add would
// otherwise leave the stale declaration behind silently.
func warnOtherTable(opts Options, editor *manifest.Editor, target manifest.DepTable, name string) {
	if opts.Warn == nil {
		return
	}

	for _, table := range editor.Locate(name) {
		if table != target {
			opts.Warn(fmt.Sprintf(
				"%q is also declared in [%s]; that declaration is left as it is", name, table))
		}
	}
}

// Remove drops name's declaration from [dependencies] and [dev_dependencies]
// and re-resolves the lock without it.
//
// A name the manifest does not declare is an error, not a no-op: the argument
// is almost always a typo, and reporting success over one hides it. A name
// declared only by a component is refused for the same reason it cannot be
// added: which component owns a dependency is the user's decision, not this
// command's, so the declaration is left for them to edit by hand.
//
// Every remaining rock is pinned, so the result differs from the old lock only
// by what the removed rock was holding up.
func Remove(ctx context.Context, opts Options, name string) (*Result, error) {
	engine, err := engineFor(opts)
	if err != nil {
		return nil, err
	}

	return removeWith(ctx, opts, engine, name)
}

// RemoveFrom is Remove as tt package remove runs it: name is dropped from
// [dependencies] only, or, with dev, from [dev_dependencies] only, the way
// cargo remove and cargo remove --dev choose the table. A declaration in the
// other table stays. A name only the other table declares is refused with
// ErrNotDeclared, and the error names the table that declares it and the
// flag that removes it there.
func RemoveFrom(ctx context.Context, opts Options, name string, dev bool) (*Result, error) {
	engine, err := engineFor(opts)
	if err != nil {
		return nil, err
	}

	return removeFromWith(ctx, opts, engine, name, dev)
}

// removeFromWith is RemoveFrom against an injected resolver.
func removeFromWith(
	ctx context.Context, opts Options, res resolver, name string, dev bool,
) (*Result, error) {
	table := manifest.TableDependencies
	if dev {
		table = manifest.TableDevDependencies
	}

	return removeIn(ctx, opts, res, name, []manifest.DepTable{table})
}

// removeWith is Remove against an injected resolver.
func removeWith(
	ctx context.Context, opts Options, res resolver, name string,
) (*Result, error) {
	return removeIn(ctx, opts, res, name, nil)
}

// removeIn removes name from the tables of only, or from every table that
// declares it when only is nil, against an injected resolver.
func removeIn(
	ctx context.Context, opts Options, res resolver, name string, only []manifest.DepTable,
) (*Result, error) {
	ctx, release, err := lockProject(ctx, opts)
	if err != nil {
		return nil, err
	}

	defer release()

	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	editor, err := manifest.NewEditor(proj.source)
	if err != nil {
		return nil, stateErrorf("reading %s for editing: %w", manifestFileName, err)
	}

	located := editor.Locate(name)
	tables := located

	if only != nil {
		tables = nil

		for _, table := range located {
			if slices.Contains(only, table) {
				tables = append(tables, table)
			}
		}
	}

	if len(tables) == 0 {
		if len(located) > 0 {
			return nil, inOtherTable(name, only, located)
		}

		return nil, notDeclared(proj.manifest, name)
	}

	if len(only) == 1 {
		warnOtherTable(opts, editor, only[0], name)
	}

	var change manifest.Change

	// With no table chosen, a name in both tables is removed from both: after
	// the call the manifest must not declare it at all, which is what was
	// asked for.
	for _, table := range tables {
		one, removeErr := editor.RemoveDependency(table, name)
		if removeErr != nil {
			return nil, stateErrorf("%w", removeErr)
		}

		if !change.Existed {
			change = one
		}
	}

	warnComponentDeclarations(opts, proj.manifest, name)

	return apply(ctx, opts, res, proj, editor.Bytes(), resolve.PinsFromLock(proj.lock), change)
}

// warnComponentDeclarations reports a removed name that a component still
// declares, so the user is not surprised to find it back in the closure.
func warnComponentDeclarations(opts Options, man *manifest.Manifest, name string) {
	if opts.Warn == nil {
		return
	}

	var components []string

	for _, place := range declaredIn(man, name) {
		if strings.HasPrefix(place, "[components.") {
			components = append(components, place)
		}
	}

	if len(components) == 0 {
		return
	}

	opts.Warn(fmt.Sprintf("%q is still declared in %s and stays in the closure",
		name, strings.Join(components, ", ")))
}

// Update re-resolves the lock without changing a single declaration.
//
// With an empty name nothing is pinned: every rock takes the newest version its
// constraints allow. This is the only command that pulls newer registry
// versions — a new release upstream never makes a lock stale by itself — so it
// is also the only one whose result can differ from a plain rebuild.
//
// With a name, that one rock is freed and every other pin is held, so the diff
// is confined to it and whatever its new version requires. The name must be one
// the manifest declares, in any of its tables including a component's: an
// update names a dependency, and a dependency a component declares is one.
func Update(ctx context.Context, opts Options, name string) (*Result, error) {
	engine, err := engineFor(opts)
	if err != nil {
		return nil, err
	}

	return updateWith(ctx, opts, engine, name)
}

// updateWith is Update against an injected resolver.
func updateWith(
	ctx context.Context, opts Options, res resolver, name string,
) (*Result, error) {
	ctx, release, err := lockProject(ctx, opts)
	if err != nil {
		return nil, err
	}

	defer release()

	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	var pins resolve.Pins

	if name != "" {
		if len(declaredIn(proj.manifest, name)) == 0 {
			return nil, notDeclared(proj.manifest, name)
		}

		pins = resolve.PinsFromLock(proj.lock, name)
	} else {
		// A bare update moves every version it can, the runtime pins too.
		proj.repinRuntime = true
	}

	// A nil edit: update changes no declaration, so the manifest on disk is
	// already the one to resolve and its hash is already the right one.
	return apply(ctx, opts, res, proj, nil, pins,
		manifest.Change{Existed: false, Previous: ""})
}

// Resolve rewrites the lock from the manifest, changing no declaration and
// building nothing.
//
// It is what a user reaches for after editing [dependencies] by hand: the lock
// catches up with the manifest without a build, without fetching anything into
// .rocks/ and without pulling newer registry versions. Every version the lock
// already holds is pinned, exactly as add and remove pin them — the edit that
// prompted the run is the only thing allowed to move the closure, and pulling
// newer versions is what tt package update is for.
//
// A resolve of an already-current lock is not an error and not a no-op: the
// lock is rewritten from the same inputs, so it comes out identical and the
// caller sees no moves.
func Resolve(ctx context.Context, opts Options) (*Result, error) {
	engine, err := engineFor(opts)
	if err != nil {
		return nil, err
	}

	return resolveWith(ctx, opts, engine)
}

// resolveWith is Resolve against an injected resolver.
func resolveWith(ctx context.Context, opts Options, res resolver) (*Result, error) {
	ctx, release, err := lockProject(ctx, opts)
	if err != nil {
		return nil, err
	}

	defer release()

	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	// A nil edit, like update: the manifest on disk is already the one to
	// resolve, hash included.
	return apply(ctx, opts, res, proj, nil, resolve.PinsFromLock(proj.lock),
		manifest.Change{Existed: false, Previous: ""})
}

// inOtherTable builds the error of RemoveFrom for a name the tables of want
// do not declare and the tables of located do: where it is, and how to
// remove it there.
func inOtherTable(name string, want, located []manifest.DepTable) error {
	places := make([]string, 0, len(located))
	for _, table := range located {
		places = append(places, "["+string(table)+"]")
	}

	wanted := make([]string, 0, len(want))
	for _, table := range want {
		wanted = append(wanted, "["+string(table)+"]")
	}

	hint := "tt package remove --dev " + name
	if slices.Contains(located, manifest.TableDependencies) {
		hint = "tt package remove " + name
	}

	return stateErrorf("%w", notInTableError{
		msg: fmt.Sprintf("%q is not declared in %s but in %s; %s removes it from there",
			name, strings.Join(wanted, ", "), strings.Join(places, ", "), hint),
	})
}

// notInTableError is the error of inOtherTable. It is ErrNotDeclared for
// errors.Is without saying "not declared in the manifest", which it is.
type notInTableError struct {
	msg string
}

// Error returns the message.
func (e notInTableError) Error() string { return e.msg }

// Is reports ErrNotDeclared.
func (e notInTableError) Is(target error) bool { return target == ErrNotDeclared }

// notDeclared builds the error for a name the manifest does not declare, naming
// the component tables that do declare it when any do — the difference between
// "you mistyped it" and "it is there, but somewhere this command may not touch"
// is the whole of what the user needs to know.
func notDeclared(man *manifest.Manifest, name string) error {
	places := declaredIn(man, name)
	if len(places) == 0 {
		return stateErrorf("%q: %w", name, ErrNotDeclared)
	}

	return stateErrorf(
		"%q is declared only in %s, which this command does not edit;"+
			" change it there by hand: %w",
		name, strings.Join(places, ", "), ErrNotDeclared)
}
