package cmake

import (
	_ "embed"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/tarantool/tt/sdk/build"
)

// This file is the build of cmake without tt (build.Exporter): a script of
// sh that runs the cmake Run runs and copies what output lists, as
// copyOutputs does, the template script of export.sh.tmpl.

// exportText is export.sh.tmpl, the templates of the script of an export.
//
//go:embed export.sh.tmpl
var exportText string

// exportFuncs are the functions of the templates: q is a string as one word
// of sh (build.ShellQuote).
var exportFuncs = template.FuncMap{"q": build.ShellQuote}

// exportTemplate is exportText parsed once, a key the data has not failing
// it.
var exportTemplate = template.Must(template.New("export.sh.tmpl").Funcs(exportFuncs).
	Option("missingkey=error").Parse(exportText))

// renderScript is the template name of export.sh.tmpl executed on data, the
// view the template is written for: it fails on a mistake of the template
// alone.
func renderScript(name string, data any) (string, error) {
	var out strings.Builder

	err := exportTemplate.ExecuteTemplate(&out, name, data)
	if err != nil {
		return "", fmt.Errorf("render the script of the export: %w", err)
	}

	return out.String(), nil
}

// Export configures the working directory into TT_BUILD_DIR and builds it
// as Run does, with the prefix of the Tarantool of the script,
// TT_TARANTOOL_PREFIX, on CMAKE_PREFIX_PATH, and copies the files of output
// from the tree into the directories of their types, as copyOutputs does: a
// .so module as one of TT_MODULE_EXT, .dylib on macOS. The script loses
// nothing of Run: the tree of a build of tt that stays between builds is
// the cache the script has none of.
func (b cmakeBuild) Export(*build.ExportContext) (build.Export, error) {
	variables, prefixed := exportVariables(b.config)
	outputs := make([]outputView, 0, len(b.output))

	for _, file := range b.output {
		outputs = append(outputs, exportDest(file))
	}

	script, err := renderScript("script", scriptView{
		BuildType: buildType(b.config), Variables: variables, Prefixed: prefixed,
		Targets: b.config.Targets, ErrMissingOutput: errMissingOutput.Error(), Outputs: outputs,
	})
	if err != nil {
		return build.Export{}, err
	}

	return build.Export{Script: script, Tools: []string{cmakeTool}, Lost: nil}, nil
}

// scriptView is what the template script of export.sh.tmpl renders.
type scriptView struct {
	// BuildType is the build type of the build (buildType).
	BuildType string
	// Variables are the variables of the block, by name, and Prefixed
	// reports whether one of them is the one the script adds the prefix of
	// its Tarantool to (exportVariables).
	Variables []variableView
	Prefixed  bool
	// Targets are the targets of the block.
	Targets []string
	// ErrMissingOutput is the error the script fails with on a file of
	// output the build did not make, as copyOutputs does.
	ErrMissingOutput string
	// Outputs are the files of output, each with where the script copies
	// it (exportDest).
	Outputs []outputView
}

// variableView is a variable of the block, Name and its Value, and whether
// it is the one of CMAKE_PREFIX_PATH the script adds the prefix of its
// Tarantool to (Prefix).
type variableView struct {
	Name, Value string
	Prefix      bool
}

// exportVariables are the variables of config, by name, as configureArgs
// passes them, the first of them of CMAKE_PREFIX_PATH, of any type, marked:
// the script adds the prefix of its Tarantool to it, as configureArgs adds
// that of tt. prefixed reports whether there is one; the prefix is a
// variable of its own otherwise.
func exportVariables(config Config) ([]variableView, bool) {
	variables := make([]variableView, 0, len(config.Variables))
	prefixed := false

	for _, name := range slices.Sorted(maps.Keys(config.Variables)) {
		variable, _, _ := strings.Cut(name, ":")
		prefix := !prefixed && variable == prefixPathVariable

		prefixed = prefixed || prefix
		variables = append(variables, variableView{
			Name: name, Value: config.Variables[name], Prefix: prefix,
		})
	}

	return variables, prefixed
}

// resultWords are the variables of the directories of the results of the
// script, as words of sh, by the type of result.
var resultWords = map[build.Result]string{
	build.Module:  `"$TT_OUTPUT_DIR"`,
	build.Data:    `"$TT_SHARE_DIR"`,
	build.Program: `"$TT_PROGRAMS_DIR"`,
	build.Link:    `"$TT_LINK_DIR"`,
}

// outputView is a file of output, Path in the build tree, and where the
// script copies it: Name in the directory of its type, Root, a word of sh, a
// .so module as one of TT_MODULE_EXT (ModuleExt), Name being its stem then.
// Dir is the directory of Name the script creates first, for a file under
// a directory of that of its type; empty otherwise.
type outputView struct {
	Path, Root, Dir, Name string
	ModuleExt             bool
}

// exportDest is where the script copies file: under its base name in the
// directory of its type, as build.Dirs.OutputPath has it, a header of a
// library for linking under include/ of it, and a .so module as one of
// TT_MODULE_EXT, as copyOutputs names it on macOS.
func exportDest(file build.OutputFile) outputView {
	// OutputPath in a directory of the type of the name marker is the path
	// of the file in it.
	marker := "result"
	rel, _ := filepath.Rel(marker, build.Dirs{
		Project: "", Component: "", Cwd: "", Work: "", Modules: marker, Share: marker,
		Programs: marker, State: "", Tools: "", Tree: "", Link: marker,
	}.OutputPath(file.To, filepath.Base(file.Path)))

	rel = filepath.ToSlash(rel)

	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}

	name, moduleExt := strings.CutSuffix(rel, linuxModuleExt)
	if !moduleExt || file.To != build.Module {
		name, moduleExt = rel, false
	}

	return outputView{
		Path: file.Path, Root: resultWords[file.To], Dir: dir, Name: name, ModuleExt: moduleExt,
	}
}
