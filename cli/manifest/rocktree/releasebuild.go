package rocktree

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
)

const (
	// luaExt ends the name of a Lua file, the one LuaRocks renames to the
	// last segment of the module name it is installed as.
	luaExt = ".lua"
	// lineWidth is the width the lines of a long string of the rockspec are
	// kept within (luaPieces), and indent begins the lines it is continued
	// on.
	lineWidth = 80
	indent    = "      "
	// concat is the Lua operator that ends a line a string is continued
	// after.
	concat = " .."
)

var (
	// ErrInstallDest reports a path in a rock that build.install cannot say
	// (CheckInstallDest).
	ErrInstallDest = errors.New("build.install of a rockspec cannot say the path")
	// errInstallTwice reports two files of build.install at one path in the
	// rock.
	errInstallTwice = errors.New("two files are installed at the path")
	// errInstallName reports a file of build.install whose source LuaRocks
	// would install under another name than its path in the rock says.
	errInstallName = errors.New("LuaRocks installs the file under another name")
)

// ReleaseBuild is how LuaRocks builds the rock of a release from its sources
// (Release.Build): the build table of its rockspec. tt reads none of it, as
// tt builds the package from its manifest; it is there for LuaRocks, tt rocks
// of tt 2 among them, which knows no manifest. The zero value builds
// nothing: the build type none and no file to install.
type ReleaseBuild struct {
	// Install are the files of the sources LuaRocks installs into the rock,
	// each at its path in the rock: build.install, of build type none.
	Install []ReleaseFile
	// ExtraFiles are the files the rockspec carries, by name, which LuaRocks
	// writes at the root of the sources before it builds: build.extra_files.
	// They are the files a build of the package generates, which neither the
	// source rock nor the repository of the package holds, so that a build
	// from either installs them as the binary rock has them. A file of
	// Install names one by its name.
	ExtraFiles map[string]string
	// TtOnly, when not empty, says why LuaRocks cannot build the package,
	// which only tt builds: the build, of type command, prints it to stderr
	// and fails, and Install and ExtraFiles are left out.
	TtOnly string
}

// ReleaseFile is a file LuaRocks installs from the sources of a rock into it
// (ReleaseBuild.Install).
type ReleaseFile struct {
	// Source is the path of the file in the sources, slash-separated: its
	// path in the project, or the name of an extra file.
	Source string
	// Dest is the path of the file in the rock, slash-separated: under lua/
	// (StageLua) or lib/ (StageLib), as the stage of the product a build lays
	// out has it. A file that is not Lua keeps the name of its source, as
	// LuaRocks names it so, and one that is, of a name ending in .lua, is
	// Lua at either end.
	Dest string
}

// CheckInstallDest reports, wrapping ErrInstallDest, a path dest in a rock,
// slash-separated, that build.install of a rockspec cannot say. LuaRocks
// installs the files of lua/ and lib/ by a module name, whose dots are the
// slashes of the path: the directory is all of the name but its last
// segment, and a Lua file is named by that segment and .lua, another file by
// the name of its source. So a directory whose name has a dot cannot be said,
// nor a Lua file whose name has one before .lua, or none before it at all.
func CheckInstallDest(dest string) error {
	group, rel, _ := strings.Cut(dest, "/")
	if group != StageLua && group != StageLib {
		return fmt.Errorf("%w %s: build.install installs into lua/ and lib/ alone",
			ErrInstallDest, dest)
	}

	parts := strings.Split(rel, "/")

	for _, dir := range parts[:len(parts)-1] {
		if dir == "" || strings.Contains(dir, ".") {
			return fmt.Errorf("%w %s: the name of the directory %q has a dot, which a "+
				"module name of LuaRocks says a slash with", ErrInstallDest, dest, dir)
		}
	}

	name := parts[len(parts)-1]
	if name == "" {
		return fmt.Errorf("%w %s: the path names no file", ErrInstallDest, dest)
	}

	stem, lua := strings.CutSuffix(name, luaExt)
	if lua && (stem == "" || strings.Contains(stem, ".")) {
		return fmt.Errorf("%w %s: the name of the Lua file %q has a dot before .lua, or "+
			"nothing, and a module name of LuaRocks names the file with what follows "+
			"its last dot", ErrInstallDest, dest, name)
	}

	return nil
}

// writeBuild writes the build table of the rockspec of build. A build of tt
// alone (ReleaseBuild.TtOnly) is of type command and fails, printing why;
// any other is of type none, with the tables of build.install and
// build.extra_files it has.
func writeBuild(src *strings.Builder, build ReleaseBuild) error {
	src.WriteString("build = {\n")

	if build.TtOnly != "" {
		const field = "   build_command = "

		src.WriteString("   type = \"command\",\n")
		src.WriteString(field)

		pieces := luaPieces(failCommand(build.TtOnly), lineWidth-len(field), lineWidth-len(indent))
		for index, piece := range pieces {
			if index > 0 {
				src.WriteString(concat + "\n" + indent)
			}

			src.WriteString(piece)
		}

		src.WriteString(",\n}\n")

		return nil
	}

	src.WriteString("   type = \"none\",\n")

	tables, err := installTables(build.Install)
	if err != nil {
		return err
	}

	if len(tables) > 0 {
		src.WriteString("   install = {\n")

		for _, group := range []string{StageLua, StageLib} {
			table := tables[group]
			if len(table) == 0 {
				continue
			}

			fmt.Fprintf(src, "      %s = {\n", group)

			for _, key := range slices.Sorted(maps.Keys(table)) {
				fmt.Fprintf(src, "         [%s] = %s,\n", luaQuote(key), luaQuote(table[key]))
			}

			src.WriteString("      },\n")
		}

		src.WriteString("   },\n")
	}

	if len(build.ExtraFiles) > 0 {
		src.WriteString("   extra_files = {\n")

		for _, name := range slices.Sorted(maps.Keys(build.ExtraFiles)) {
			fmt.Fprintf(src, "      [%s] = %s,\n", luaQuote(name),
				luaLongString(build.ExtraFiles[name]))
		}

		src.WriteString("   },\n")
	}

	src.WriteString("}\n")

	return nil
}

// installTables makes the tables of build.install that install files, by
// group, lua and lib, and in each by key: the module name a file is installed
// as (CheckInstallDest), its source the value.
//
// The key of a Lua file is its path in the group, slashes as dots and
// without .lua. The key of another file says its directory alone, and its
// last segment is any free one: the name of the file up to its first dot,
// that name with its dots as underscores, then that one numbered from 2, the
// first that no Lua file and no file before it in order of path has.
func installTables(files []ReleaseFile) (map[string]map[string]string, error) {
	tables := map[string]map[string]string{}

	table := func(group string) map[string]string {
		if tables[group] == nil {
			tables[group] = map[string]string{}
		}

		return tables[group]
	}

	dests := map[string]bool{}

	var others []ReleaseFile

	for _, file := range files {
		err := checkInstallFile(file)
		if err != nil {
			return nil, err
		}

		if dests[file.Dest] {
			return nil, fmt.Errorf("build.install: %w %s", errInstallTwice, file.Dest)
		}

		dests[file.Dest] = true

		group, rel, _ := strings.Cut(file.Dest, "/")
		if !strings.HasSuffix(rel, luaExt) {
			others = append(others, file)

			continue
		}

		table(group)[strings.ReplaceAll(strings.TrimSuffix(rel, luaExt), "/", ".")] = file.Source
	}

	slices.SortFunc(others, func(a, b ReleaseFile) int { return strings.Compare(a.Dest, b.Dest) })

	for _, file := range others {
		group, rel, _ := strings.Cut(file.Dest, "/")

		prefix := ""
		if dir := path.Dir(rel); dir != "." {
			prefix = strings.ReplaceAll(dir, "/", ".") + "."
		}

		entries := table(group)

		entries[freeKey(entries, prefix, path.Base(rel))] = file.Source
	}

	return tables, nil
}

// checkInstallFile reports a file build.install cannot install where Dest
// says: a path it cannot say (CheckInstallDest), or a source LuaRocks names
// the file in the rock otherwise by - a Lua file by the module name, another
// one by its own name.
func checkInstallFile(file ReleaseFile) error {
	err := CheckInstallDest(file.Dest)
	if err != nil {
		return err
	}

	source, dest := path.Base(file.Source), path.Base(file.Dest)

	switch {
	case strings.HasSuffix(source, luaExt) != strings.HasSuffix(dest, luaExt),
		!strings.HasSuffix(source, luaExt) && source != dest:
		return fmt.Errorf("build.install: %w: %s as %s", errInstallName, file.Source, file.Dest)
	default:
		return nil
	}
}

// freeKey is the key of a file that is not Lua, name, in the directory the
// key prefix says (installTables): the first of its candidate last segments
// table has no key of.
func freeKey(table map[string]string, prefix, name string) string {
	stem, _, _ := strings.Cut(name, ".")
	whole := strings.ReplaceAll(name, ".", "_")

	for _, segment := range []string{stem, whole} {
		if _, taken := table[prefix+segment]; segment != "" && !taken {
			return prefix + segment
		}
	}

	for number := 2; ; number++ {
		key := prefix + whole + "_" + strconv.Itoa(number)
		if _, taken := table[key]; !taken {
			return key
		}
	}
}

// luaPieces quotes text as Lua string literals (luaQuote) that make it when
// joined by .., for a line of first columns and the lines after it of rest:
// text is split after its spaces, as few times as a piece and the operator
// after it fit a line. A word that does not fit a line alone is a piece of its
// own.
func luaPieces(text string, first, rest int) []string {
	var pieces []string

	width, piece := first, ""

	for word := range strings.SplitAfterSeq(text, " ") {
		if piece != "" && len(luaQuote(piece+word))+len(concat) > width {
			pieces = append(pieces, luaQuote(piece))
			width, piece = rest, ""
		}

		piece += word
	}

	return append(pieces, luaQuote(piece))
}

// failCommand is the command of POSIX sh that prints message to stderr and
// fails, for a build of type command. LuaRocks substitutes its variables,
// $(NAME), in the command before it runs it, so the quoting of the message
// leaves none of them in it (shellQuote).
func failCommand(message string) string {
	return "printf '%s\\n' " + shellQuote(message) + " >&2; exit 1"
}

// shellQuote quotes text as one word of POSIX sh, in single quotes, which
// hold anything but a single quote: that one closes them, is escaped and
// opens them again. A $( in text is split by a closing and an opening quote
// too, so that LuaRocks, which substitutes its $(NAME) variables in a
// command whatever its quotes, finds none.
func shellQuote(text string) string {
	quoted := strings.ReplaceAll(strings.ReplaceAll(text, "'", `'\''`), "$(", "$''(")

	return "'" + quoted + "'"
}

// luaLongString renders content as a Lua long string literal, which holds it
// as it is: a newline follows the opening bracket, which Lua drops, and the
// level of the brackets is the lowest from 1 whose closing bracket content
// neither holds nor ends with the beginning of. Level 0 is never taken: Lua
// 5.1 fails on a [[ in a string of level 0. A content with a carriage return,
// which Lua reads as a newline in a long string, is a quoted string instead
// (luaQuote).
func luaLongString(content string) string {
	if strings.Contains(content, "\r") {
		return luaQuote(content)
	}

	level := "="
	for strings.Contains(content+"]", "]"+level+"]") {
		level += "="
	}

	return "[" + level + "[\n" + content + "]" + level + "]"
}
