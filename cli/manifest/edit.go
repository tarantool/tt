package manifest

import (
	"bytes"
	"errors"
	"fmt"
)

// This file is the writing half of the manifest data layer: it edits the
// [dependencies]/[dev_dependencies] tables of an app.manifest.toml *in its own
// source text* instead of re-marshaling the parsed model.
//
// Re-marshaling is not an option. go-toml keeps no comments, no key order and
// no formatting in its AST, so Manifest.Marshal() returns a canonical document
// that shares only the data with what the user wrote. app.manifest.toml is a
// hand-authored file - people comment their pins and group their sections -
// and manifest_hash is taken over the raw bytes, so a reformat is both rude
// and semantically loud.
//
// So the approach is the one `go mod edit -require` uses: parse the source to
// find the exact byte range of the entry being touched, splice the text, and
// leave every other byte alone. The locating is done with
// go-toml/v2/unstable's streaming expression parser, which reports the
// top-level expressions of a document in source order along with the input
// ranges of their key and value tokens.

// DepTable names a dependency table an edit targets.
//
// Only the two document-level tables are addressable. A component's own
// [components.<name>.dependencies] is deliberately out of reach: which
// component a dependency belongs to is a decision `tt package add` does not
// get to make for the user.
type DepTable string

const (
	// TableDependencies is [dependencies]: what the package needs to run.
	TableDependencies DepTable = "dependencies"
	// TableDevDependencies is [dev_dependencies]: what only the package's own
	// tests and tooling need.
	TableDevDependencies DepTable = "dev_dependencies"
)

// ErrNoSuchDependency is returned by RemoveDependency when the target table
// does not declare the name at all. SetDependency never returns it - a missing
// name is what it is there to add. Callers match it with errors.Is.
var ErrNoSuchDependency = errors.New("dependency is not declared")

// ErrUnsupportedForm is returned when a declaration is written in a shape this
// editor refuses to rewrite: a value that is neither a constraint string nor a
// table, a table nested below the dependency, the same name declared twice, or
// a request to set a version on a declaration that has no version key.
//
// The refusal is deliberate and is the whole safety model of this file. A
// wrong splice destroys a file the user hand-wrote; a refusal costs them one
// hand edit. Every error wrapping this sentinel names the construct that
// caused it.
var ErrUnsupportedForm = errors.New("dependency declaration cannot be edited automatically")

// Change reports what an edit did to an existing declaration. A zero Change
// means the edit created a declaration that was not there before.
type Change struct {
	Existed  bool   // A declaration was already there.
	Previous string // Its previous version constraint, when Existed.
}

// Editor rewrites the dependency tables of a manifest's TOML source in place,
// leaving every untouched byte of the file exactly as it was.
//
// It is not safe for concurrent use: every method re-indexes the current
// source, so an edit must complete before the next one starts.
type Editor struct {
	src []byte
}

// NewEditor parses src for editing. The source has to be syntactically valid
// TOML; it does not have to be a valid manifest, because a caller may well be
// repairing one. Callers keep ownership of src: the Editor copies it.
func NewEditor(src []byte) (*Editor, error) {
	_, err := indexSource(src)
	if err != nil {
		return nil, err
	}

	return &Editor{src: bytes.Clone(src)}, nil
}

// Bytes returns the edited source. The returned slice is a copy, so a caller
// can hold on to an intermediate state across further edits.
func (e *Editor) Bytes() []byte {
	return bytes.Clone(e.src)
}

// Locate reports which dependency tables declare name, in a stable order
// ([dependencies] before [dev_dependencies]).
//
// It reports presence, not editability: a name declared in a form
// SetDependency would refuse is still located, because `tt package add` has to
// tell "already there, in a shape I cannot touch" apart from "not there".
func (e *Editor) Locate(name string) []DepTable {
	doc, err := indexSource(e.src)
	if err != nil {
		// The source was valid when the Editor was built and every edit path
		// re-checks its own output, so this is unreachable; an empty result is
		// the honest answer rather than a panic.
		return nil
	}

	var found []DepTable

	for _, table := range []DepTable{TableDependencies, TableDevDependencies} {
		if doc.declares(table, name) {
			found = append(found, table)
		}
	}

	return found
}

// SetDependency declares name in table with the given version constraint, or
// rewrites the constraint of an existing declaration.
//
// What it rewrites depends on the form found:
//
//   - short form (name = ">=1.0.0"): the string value is replaced;
//   - inline table (name = { source = "registry", version = ">=1.0.0" }) and
//     sub-table ([dependencies.name] with its own lines): the value of the
//     version key is replaced, and nothing else in the declaration moves;
//   - a declaration with no version key - a path dependency, say - is refused
//     with ErrUnsupportedForm. Pinning a version on a path dependency is
//     contradictory, so guessing where the key should go would be wrong.
//
// When name is not declared yet it is appended as a short-form entry after the
// last entry of the target table, or, when the table does not exist at all, in
// a new section at the end of the file. Insertion is positional on purpose: a
// section that happens to be sorted is not evidence that the user wants it
// kept sorted, and reordering is the kind of diff noise this editor exists to
// avoid.
func (e *Editor) SetDependency(table DepTable, name, constraint string) (Change, error) {
	var change Change

	err := checkEditTarget(table, name)
	if err != nil {
		return change, err
	}

	if constraint == "" {
		return change, invalid(string(table)+"."+name, "version constraint is empty")
	}

	doc, err := indexSource(e.src)
	if err != nil {
		return change, err
	}

	decl, err := doc.find(table, name)

	switch {
	case errors.Is(err, ErrNoSuchDependency):
		e.src = doc.insert(table, name, constraint)

		return change, nil
	case err != nil:
		return change, err
	}

	if !decl.hasVersion {
		return change, fmt.Errorf("%s.%s: %w: the declaration carries no version key,"+
			" edit it by hand", table, name, ErrUnsupportedForm)
	}

	change.Existed = true
	change.Previous = decl.versionData
	e.src = doc.replace(decl.version, quoteTOMLString(constraint))

	return change, nil
}

// RemoveDependency drops name's declaration from table.
//
// The whole declaration goes: for a sub-table that is the [dependencies.name]
// header and every line of its body, up to and including its last key - a
// following table header, and any comment lines between that last key and it,
// stay.
//
// Comment lines above the removed declaration are also left in place. Whether
// a comment documents the entry below it or the section it sits in is
// guesswork, and deleting a line the user wrote on a guess is worse than
// leaving a stale one they can see and remove.
//
// Removing the last entry of a table leaves the now-empty table header behind,
// which parses to an empty map and validates.
func (e *Editor) RemoveDependency(table DepTable, name string) (Change, error) {
	var change Change

	err := checkEditTarget(table, name)
	if err != nil {
		return change, err
	}

	doc, err := indexSource(e.src)
	if err != nil {
		return change, err
	}

	decl, err := doc.find(table, name)
	if err != nil {
		return change, err
	}

	change.Existed = true
	change.Previous = decl.versionData
	e.src = doc.cut(doc.removalSpans(decl))

	return change, nil
}

// checkEditTarget rejects arguments no edit can act on before any parsing is
// done, so a typo in a table name cannot be mistaken for a missing dependency.
func checkEditTarget(table DepTable, name string) error {
	switch table {
	case TableDependencies, TableDevDependencies:
	default:
		return invalid(string(table), "not a dependency table")
	}

	if name == "" {
		return invalid(string(table), "dependency name is empty")
	}

	return nil
}
