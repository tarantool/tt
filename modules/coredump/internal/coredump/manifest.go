package coredump

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// manifestFile is the manifest of a directory of debugger extensions, in
// the schema of github.com/buyemune-sys/tarantool-gdb-tools: tt describes
// the copies it embeds with it, and reads a manifest of that repository the
// same way. Where a file comes from is no part of the schema.
const manifestFile = "extensions.json"

// manifestSchema is the version of the schema of the manifest this reader
// knows.
const manifestSchema = 1

var (
	// errManifestSchema reports a manifest of a schema the reader does not
	// know.
	errManifestSchema = errors.New("unknown schema of the extension manifest")
	// errManifestFile reports an entry of a manifest that names no file of
	// its directory, or a file an entry before it names.
	errManifestFile = errors.New("bad file in the extension manifest")
	// errManifestAfter reports an entry of a manifest to load after a file
	// the manifest does not have.
	errManifestAfter = errors.New("unknown file in after of the extension manifest")
	// errLoadCycle reports extensions each of which is to load after
	// another of them.
	errLoadCycle = errors.New("the extensions load after each other")
)

// manifest is a manifest of debugger extensions. A field the reader does
// not know is ignored: the schema may grow.
type manifest struct {
	// Schema is the version of the schema, manifestSchema.
	Schema int `json:"schema"`
	// Extensions are the extensions of the directory.
	Extensions []manifestEntry `json:"extensions"`
}

// manifestEntry is an extension of a manifest.
type manifestEntry struct {
	// File is the name of its file, in the directory of the manifest.
	File string `json:"file"`
	// Debuggers are the debuggers the file loads in: gdb, lldb.
	Debuggers []string `json:"debuggers"`
	// After are the files of the manifest that are loaded before this one.
	After []string `json:"after"`
	// Tarantool is the range of the versions of Tarantool the file is known
	// to match: every minor version in it passed the CI of the file on its
	// latest patch, on every architecture of Arch.
	Tarantool versionRange `json:"tarantool"`
	// Arch are the architectures the file works on: x86_64, aarch64.
	Arch []string `json:"arch"`
	// Commands are the commands the file registers in every debugger of
	// Debuggers, by their full names as they are typed: "info tt-fibers".
	// Its parameters, pretty-printers, unwinders, frame filters and
	// convenience functions are not listed.
	Commands []string `json:"commands"`
	// Parameters are the parameters the file registers, by their full
	// names as they are typed after set and show: "print tt-list-entry".
	// A manifest may leave them out when the file registers none.
	Parameters []string `json:"parameters"`
}

// versionRange is a range of the versions of Tarantool, x.y.z, both ends
// included.
type versionRange struct {
	// Min is the first version of the range.
	Min string `json:"min"`
	// Max is the last version of the range.
	Max string `json:"max"`
}

// parseManifest reads a manifest and returns its entries in their load
// order (see loadOrder). A manifest of another schema than manifestSchema
// is an error, and so is an entry whose file is no name of a file or the
// file of an entry before it, and an entry to load after a file the
// manifest does not have or, through others, after itself.
func parseManifest(data []byte) ([]manifestEntry, error) {
	var parsed manifest

	err := json.Unmarshal(data, &parsed)
	if err != nil {
		return nil, fmt.Errorf("cannot read the extension manifest: %w", err)
	}

	if parsed.Schema != manifestSchema {
		return nil, fmt.Errorf("%w: %d, this tt reads %d", errManifestSchema, parsed.Schema,
			manifestSchema)
	}

	files := make(map[string]bool, len(parsed.Extensions))

	for _, entry := range parsed.Extensions {
		if !isFileName(entry.File) || files[entry.File] {
			return nil, fmt.Errorf("%w: %q", errManifestFile, entry.File)
		}

		files[entry.File] = true
	}

	for _, entry := range parsed.Extensions {
		for _, file := range entry.After {
			if !files[file] {
				return nil, fmt.Errorf("%w: %s is to load after %q", errManifestAfter, entry.File,
					file)
			}
		}
	}

	return loadOrder(parsed.Extensions,
		func(entry manifestEntry) string { return entry.File },
		func(entry manifestEntry) []string { return entry.After })
}

// isFileName reports whether name is the name of a file in a directory,
// with no directory of its own.
func isFileName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// loadOrder returns items in the order they are loaded in: each after the
// items whose names its after lists, and otherwise in the order of items,
// the first that may load next first. A name no item has orders nothing:
// what is not loaded is not waited for. Items each of which is to load
// after another of them are an error.
func loadOrder[T any](items []T, nameOf func(T) string, afterOf func(T) []string,
) ([]T, error) {
	present := make(map[string]bool, len(items))

	for _, item := range items {
		present[nameOf(item)] = true
	}

	loaded := make(map[string]bool, len(items))
	ready := func(item T) bool {
		for _, name := range afterOf(item) {
			if present[name] && !loaded[name] {
				return false
			}
		}

		return true
	}

	done := make([]bool, len(items))
	ordered := make([]T, 0, len(items))

	for len(ordered) < len(items) {
		next := -1

		for i, item := range items {
			if !done[i] && ready(item) {
				next = i

				break
			}
		}

		if next < 0 {
			var waiting []string

			for i, item := range items {
				if !done[i] {
					waiting = append(waiting, nameOf(item))
				}
			}

			return nil, fmt.Errorf("%w: %s", errLoadCycle, strings.Join(waiting, ", "))
		}

		done[next] = true
		loaded[nameOf(items[next])] = true
		ordered = append(ordered, items[next])
	}

	return ordered, nil
}
