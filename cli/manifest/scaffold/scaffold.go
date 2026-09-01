// Package scaffold writes the skeleton app.manifest.toml that tt new creates.
//
// It is deliberately small and deliberately not cli/create: that command lays
// out a whole application from a template, while this one writes a single file
// into a directory that may already hold a project. A manifest is the one thing
// every tt package command needs and the one thing a rockspec-era project does
// not have, so tt new is the smallest possible step into the manifest pipeline
// rather than a project generator.
//
// What it writes is the minimum a manifest needs to be valid — the format
// version, the package identity, the platform requirements and an empty
// [dependencies] table for tt package add to write into. Components and
// products, which a package needs before it can be built, are left as commented
// examples: guessing a layout for an existing directory would be wrong more
// often than right, and a wrong guess costs more than an absent one.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/build"
)

// manifestFileName is the file tt new creates, next to which every other
// package command expects to run.
const manifestFileName = "app.manifest.toml"

// filePerm is the mode the new manifest is created with: a hand-edited source
// file, like the rest of a project's sources.
const filePerm os.FileMode = 0o644

// suggestedPackageName is what the error suggests when the directory name
// cannot be turned into a package name at all. It is always valid, so the
// message always names something the user can pass straight back.
const suggestedPackageName = "app"

// exitStateError is the process exit code for a usage or state failure, the
// same one every other package command uses.
const exitStateError = 1

// ErrManifestExists reports a directory that already holds a manifest. tt new
// refuses rather than overwriting: the existing file is hand-authored, may hold
// a whole project's dependencies, and nothing about "create a skeleton" implies
// consent to lose it.
var ErrManifestExists = errors.New("a manifest already exists")

// ExitError re-exports build.ExitError so tt new returns the same typed error
// the other package commands do.
type ExitError = build.ExitError

// ExitCode returns the process exit code for err, reusing the build package's
// mapping so every manifest command agrees. A nil error is 0.
func ExitCode(err error) int {
	return build.ExitCode(err)
}

// Options configures one tt new run.
type Options struct {
	// ProjectDir is the directory the manifest is created in. Required and must
	// be absolute.
	ProjectDir string
	// Name is the package name to declare. Empty takes the name from the
	// directory, which then has to be a package name already.
	Name string
}

// Result reports what was created.
type Result struct {
	// Path is the manifest that was written.
	Path string
	// Package is the package name it declares.
	Package string
}

// Create writes the skeleton manifest into opts.ProjectDir.
//
// The package name is opts.Name, or the directory name when that is empty. A
// directory whose name is not already a package name is refused, with the name
// it would have to be: silently rewriting "My_App" to "my-app" would put a name
// in the manifest that the user never chose and would not think to look for.
//
// An existing manifest is never overwritten. The file is created with O_EXCL,
// so the check and the write are one operation and a concurrent run cannot slip
// between them.
func Create(opts Options) (*Result, error) {
	name, err := packageNameFor(opts)
	if err != nil {
		return nil, err
	}

	source := render(name)

	// Generate, then validate what was generated: this file is the one thing
	// standing between tt new and every later command, and a skeleton that does
	// not parse would only be discovered by the next command the user runs.
	err = validate(source)
	if err != nil {
		return nil, stateErrorf("the generated manifest is not valid: %w", err)
	}

	path := filepath.Join(opts.ProjectDir, manifestFileName)

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, stateErrorf("%s: %w", manifestFileName, ErrManifestExists)
		}

		return nil, stateErrorf("creating %s: %w", manifestFileName, err)
	}

	_, err = file.WriteString(source)
	if err != nil {
		// Leaves a truncated file behind, which the next run then refuses. That
		// is the honest outcome: the write failed for a reason (a full disk, a
		// read-only mount) that removing the file would not fix and might hide.
		file.Close()

		return nil, stateErrorf("writing %s: %w", manifestFileName, err)
	}

	err = file.Close()
	if err != nil {
		return nil, stateErrorf("writing %s: %w", manifestFileName, err)
	}

	return &Result{Path: path, Package: name}, nil
}

// packageNameFor resolves the package name to declare: the one given, or the
// project directory's own name.
//
// A name that is not a package name is refused either way. When it came from
// the directory the message carries the name it would have to be, so the fix is
// one flag away; when the user typed it, the shape rules are the answer.
func packageNameFor(opts Options) (string, error) {
	if opts.Name != "" {
		if !manifest.ValidPackageName(opts.Name) {
			return "", stateErrorf(
				"%q is not a package name: use lowercase letters, digits and dashes,"+
					" starting with a letter", opts.Name)
		}

		return opts.Name, nil
	}

	dirName := filepath.Base(opts.ProjectDir)
	if manifest.ValidPackageName(dirName) {
		return dirName, nil
	}

	suggestion := normalizeName(dirName)
	if suggestion == "" {
		suggestion = suggestedPackageName
	}

	return "", stateErrorf("%q is not a package name; run: tt new -n %s", dirName, suggestion)
}

// normalizeName turns a directory name into the package name it would have to
// be, or returns "" when no such name falls out of it.
//
// Package names are [a-z][a-z0-9-]*, so the mapping lowercases, replaces every
// other character with a dash, and collapses the runs a replacement produces —
// "My App (v2)" becomes "my-app-v2". A name that still does not fit the shape
// afterwards, or that is one of the reserved names, yields nothing rather than
// being mangled further: "2do" could become "app-2do", but a suggestion with an
// invented prefix is worse than no suggestion.
func normalizeName(dirName string) string {
	var out strings.Builder

	for _, r := range strings.ToLower(dirName) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out.WriteRune(r)
		default:
			out.WriteByte('-')
		}
	}

	name := collapseDashes(out.String())
	if !manifest.ValidPackageName(name) {
		return ""
	}

	return name
}

// collapseDashes squeezes runs of dashes into one and trims the ends, so a name
// built by replacing punctuation does not carry the punctuation's shape.
func collapseDashes(name string) string {
	parts := strings.Split(name, "-")

	kept := make([]string, 0, len(parts))

	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}

	return strings.Join(kept, "-")
}

// validate parses and validates generated source the way every command that
// reads a manifest will.
func validate(source string) error {
	man, warnings, err := manifest.ParseManifest([]byte(source))
	if err != nil {
		return err //nolint:wrapcheck // Caller wraps with the file it came from.
	}

	if len(warnings) > 0 {
		return fmt.Errorf("%w: %s", errUnexpectedWarning, strings.Join(warnings, "; "))
	}

	_, err = man.Validate()

	return err //nolint:wrapcheck // Caller wraps with the file it came from.
}

// errUnexpectedWarning guards against a template this build of tt would itself
// warn about — an unknown field, say, left behind by a format bump.
var errUnexpectedWarning = errors.New("the generated manifest produced warnings")

// stateErrorf wraps a formatted error as a state error (exit 1).
//
//nolint:err113 // Formatting helper, mirrors fmt.Errorf; callers pass %w wraps.
func stateErrorf(format string, args ...any) error {
	return &ExitError{Code: exitStateError, Err: fmt.Errorf(format, args...)}
}
