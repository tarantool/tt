package manifest

import (
	"strconv"
	"strings"
	"unicode"
)

// Replacement is one entry of [replace] of a distribution: a Go module that
// the build replaces with a local directory or with another module version,
// the way a replace directive of a go.mod does. The go command applies the
// replace directives of the main module only, so a tt module that depends on
// a library that is not published, or not at the version it needs, cannot
// carry the replacement itself.
type Replacement struct {
	// Key is the key as the manifest writes it: Module, or Module@Version.
	Key string
	// Module is the Go module path that is replaced.
	Module string
	// Version is the version that is replaced; empty replaces every version.
	Version string
	// Path is the local directory the module is replaced with, as the
	// manifest names it; empty for a replacement with a module.
	Path string
	// Target is the module the module is replaced with; empty for a
	// directory.
	Target string
	// TargetVersion is the Go version query of Target.
	TargetVersion string
}

// IsPath reports whether the module is replaced with a local directory.
func (r Replacement) IsPath() bool {
	return r.Path != ""
}

// Replacements returns the entries of [replace] in the order of their keys.
// It must be called on a validated distribution: an entry that does not
// parse is left out.
func (d *Distribution) Replacements() []Replacement {
	keys := sortedKeys(d.Replace)
	out := make([]Replacement, 0, len(keys))

	for _, key := range keys {
		rep, err := ParseReplacement(key, d.Replace[key])
		if err != nil {
			continue
		}

		out = append(out, rep)
	}

	return out
}

// ParseReplacement parses one entry of [replace]. The key is a module path,
// optionally followed by @ and the version it is limited to. The value is a
// local directory - it starts with ./, ../ or / - or a module path followed
// by @ and a Go version query.
func ParseReplacement(key, value string) (Replacement, error) {
	rep := Replacement{
		Key: key, Module: key, Version: "", Path: "", Target: "", TargetVersion: "",
	}

	if module, version, found := strings.Cut(key, "@"); found {
		rep.Module, rep.Version = module, version

		if version == "" || strings.Contains(version, "@") ||
			strings.ContainsFunc(version, unicode.IsSpace) {
			return Replacement{}, invalid(replaceField(key),
				"%q is not a version: want <module> or <module>@<version>", version)
		}
	}

	if !isModulePath(rep.Module) {
		return Replacement{}, invalid(replaceField(key), "%q is not a Go module path", rep.Module)
	}

	switch {
	case isLocalPath(value):
		rep.Path = value
	case strings.Contains(value, "@"):
		target, query, _ := strings.Cut(value, "@")

		if !isModulePath(target) || query == "" || strings.Contains(query, "@") ||
			strings.ContainsFunc(query, unicode.IsSpace) {
			return Replacement{}, invalid(replaceField(key),
				"%q is neither a directory nor <module>@<version>", value)
		}

		rep.Target, rep.TargetVersion = target, query
	default:
		return Replacement{}, invalid(replaceField(key),
			"%q is neither a directory (it starts with ./, ../ or /) nor <module>@<version>", value)
	}

	return rep, nil
}

// replaceField is the name of the entry key of [replace] in an error.
func replaceField(key string) string {
	return "replace." + strconv.Quote(key)
}

// validateReplace checks [replace]. Whether the module is one the build
// takes from elsewhere - the core's repository, a [dependencies] module
// given by a path - is checked by the build, which knows them.
func (d *Distribution) validateReplace() error {
	for _, key := range sortedKeys(d.Replace) {
		rep, err := ParseReplacement(key, d.Replace[key])
		if err != nil {
			return err
		}

		field := replaceField(key)

		if rep.Module == d.CoreModule() {
			return invalid(field, "is the core: replace it with [core].path, or build with "+
				"--core, not with [replace]")
		}

		for _, name := range sortedKeys(d.Dependencies) {
			if d.Dependencies[name].Module == rep.Module {
				return invalid(field, "is the tt module %q of [dependencies]: give that "+
					"module a path or a version there instead", name)
			}
		}
	}

	return nil
}
