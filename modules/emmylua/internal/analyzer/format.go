package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// FormatLevel is the language level tt emmylua fmt gives luafmt unless told
// another: the Lua of runtimeVersion, which the configuration of the analyzer
// gives EmmyLua, as luafmt's --level names it. luafmt parses Lua 5.5 by
// default, and fails on the literals of LuaJIT, such as 0ULL.
const FormatLevel = "lua-jit"

// formatConfigNames are the configurations of luafmt, in the order it looks
// for them in a directory.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var formatConfigNames = []string{".luafmt.toml", "luafmt.toml"}

// FormatLevelConfigured reports whether a configuration luafmt reads to
// format paths sets the language level, [syntax] level: luafmt takes --level
// over it, so FormatLevel is for a run where none does. With explicit, the
// file of luafmt's --config, that is the configuration of every file.
// Otherwise it is, for each path, the one luafmt finds for it: the nearest
// .luafmt.toml or luafmt.toml in the path itself for a directory, in the
// directory of a file, or in a directory above; and above dir, the directory
// luafmt runs in, for a path with none. A relative path is taken from dir.
//
// luafmt looks for the configuration of each file it formats, so a deeper
// one governs the files under it; one --level cannot follow them all, and
// those are not looked at. A configuration tt cannot read counts as setting
// the level, so that it overrides nothing the file may say: luafmt reports
// what is wrong with it, and tt reads no YAML.
func FormatLevelConfigured(dir string, paths []string, explicit string) bool {
	if explicit != "" {
		return setsFormatLevel(absFrom(dir, explicit))
	}

	for _, path := range paths {
		config := findFormatConfig(absFrom(dir, path))
		if config == "" {
			config = findFormatConfig(dir)
		}

		if config != "" && setsFormatLevel(config) {
			return true
		}
	}

	return false
}

// SplitFormatLevel splits files, relative to dir, the directory luafmt runs
// in, by whether the configuration luafmt finds for each sets the language
// level, as FormatLevelConfigured tells it for a file: set holds the files
// whose configuration does, unset the others, each in the order of files. A
// file list follows the configuration of every file, which one --level for
// a directory luafmt walks cannot: the files of set run without FormatLevel,
// those of unset with it. The configuration of a directory is looked for
// once.
func SplitFormatLevel(dir string, files []string) ([]string, []string) {
	var set, unset []string

	known := make(map[string]bool)

	for _, file := range files {
		parent := filepath.Dir(absFrom(dir, file))

		configured, seen := known[parent]
		if !seen {
			configured = FormatLevelConfigured(dir, []string{file}, "")
			known[parent] = configured
		}

		if configured {
			set = append(set, file)
		} else {
			unset = append(unset, file)
		}
	}

	return set, unset
}

// absFrom returns path, absolute: a relative one is taken from dir.
func absFrom(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(dir, path)
}

// findFormatConfig returns the configuration luafmt finds for path: the
// first of formatConfigNames in path, or in the directory of path when it
// is not a directory, and then in every directory above. It returns "" when
// there is none.
func findFormatConfig(path string) string {
	dir := path

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		dir = filepath.Dir(path)
	}

	for {
		for _, name := range formatConfigNames {
			config := filepath.Join(dir, name)

			info, err := os.Stat(config)
			if err == nil && info.Mode().IsRegular() {
				return config
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}

		dir = parent
	}
}

// setsFormatLevel reports whether the configuration of luafmt at path has
// [syntax] level, read as luafmt reads it: TOML or JSON by its extension, and
// TOML, then JSON for another. YAML, a file that cannot be read and one that
// does not parse count as having it.
func setsFormatLevel(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}

	var config map[string]any

	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		err = toml.Unmarshal(data, &config)
	case ".json":
		err = json.Unmarshal(data, &config)
	case ".yml", ".yaml":
		return true
	default:
		err = toml.Unmarshal(data, &config)
		if err != nil {
			err = json.Unmarshal(data, &config)
		}
	}

	if err != nil {
		return true
	}

	syntax, _ := config["syntax"].(map[string]any)
	_, set := syntax["level"]

	return set
}
