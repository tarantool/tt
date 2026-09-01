package deps

import (
	"context"
	"fmt"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
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
	return addWith(ctx, opts, engineFor(opts), name, constraint, dev)
}

// addWith is Add against an injected resolver.
func addWith(
	ctx context.Context, opts Options, res resolver, name, constraint string, dev bool,
) (*Result, error) {
	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	if constraint == "" {
		constraint = defaultConstraint
	}

	table := manifest.TableDependencies
	if dev {
		table = manifest.TableDevDependencies
	}

	editor, err := manifest.NewEditor(proj.source)
	if err != nil {
		return nil, stateErrorf("reading %s for editing: %w", manifestFileName, err)
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
	return removeWith(ctx, opts, engineFor(opts), name)
}

// removeWith is Remove against an injected resolver.
func removeWith(
	ctx context.Context, opts Options, res resolver, name string,
) (*Result, error) {
	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	editor, err := manifest.NewEditor(proj.source)
	if err != nil {
		return nil, stateErrorf("reading %s for editing: %w", manifestFileName, err)
	}

	tables := editor.Locate(name)
	if len(tables) == 0 {
		return nil, notDeclared(proj.manifest, name)
	}

	var change manifest.Change

	// A name in both tables is removed from both: after the command the manifest
	// must not declare it at all, which is what the user asked for.
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
	return updateWith(ctx, opts, engineFor(opts), name)
}

// updateWith is Update against an injected resolver.
func updateWith(
	ctx context.Context, opts Options, res resolver, name string,
) (*Result, error) {
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
	return resolveWith(ctx, opts, engineFor(opts))
}

// resolveWith is Resolve against an injected resolver.
func resolveWith(ctx context.Context, opts Options, res resolver) (*Result, error) {
	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	// A nil edit, like update: the manifest on disk is already the one to
	// resolve, hash included.
	return apply(ctx, opts, res, proj, nil, resolve.PinsFromLock(proj.lock),
		manifest.Change{Existed: false, Previous: ""})
}

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
