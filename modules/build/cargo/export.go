package cargo

import (
	_ "embed"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/tarantool/tt/sdk/build"
)

// This file is the build of cargo without tt (build.Exporter): a script of
// sh that runs the cargo Run runs and copies what it built as Run does, the
// template script of export.sh.tmpl.

// exportLost is what the script of a cargo build lacks against Run.
const exportLost = "the crates of a mirror of tt package download, which a build of tt " +
	"takes offline: cargo downloads those Cargo.lock pins"

// profileDirs are the directories of the target directory cargo builds the
// profiles that are not of their own name into: debug for dev and test,
// that of release for bench.
var profileDirs = map[string]string{"dev": "debug", "test": "debug", "bench": DefaultProfile}

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

// Export builds as Run does, for the machine the script runs on, into
// TT_BUILD_DIR as the target directory of cargo, which is empty when the
// script runs, so that what cargo built for the block is the one library
// and the one program there (the templates module and binary): the script
// reads no messages of cargo, and a target directory of the env of the block
// or of the environment gives way to it. The workspace is to have a
// Cargo.lock, as Preflight checks. A static program is built for the musl
// target of the machine, which is to be installed, and refused for macOS, as
// Preflight refuses it.
func (b cargoBuild) Export(*build.ExportContext) (build.Export, error) {
	triples := make([]tripleView, 0, len(cargoArches))

	for _, goarch := range slices.Sorted(maps.Keys(cargoArches)) {
		triples = append(triples, tripleView{Arch: goarch, Triple: cargoArches[goarch]})
	}

	script, err := renderScript("script", scriptView{
		Static: b.static && b.bin != "", Triples: triples, Module: b.module,
		ModuleArgs: exportArgs(b.moduleArgs("", "", 0, nil)),
		DarwinArgs: exportArgs(b.moduleArgs("", darwinOS, 0, nil)),
		ModuleName: b.config.Module, Bin: b.bin, BinArgs: exportArgs(b.args("", 0, nil)),
		ProfileDir: b.profileDir(), ErrStaticDarwin: ErrStaticDarwin.Error(),
		ErrUnsupportedTarget: errUnsupportedTarget.Error(),
		ErrNoRustTarget:      ErrNoRustTarget.Error(), ErrNoLockfile: ErrNoLockfile.Error(),
	})
	if err != nil {
		return build.Export{}, err
	}

	return build.Export{Script: script, Tools: []string{cargoTool}, Lost: []string{exportLost}},
		nil
}

// scriptView is what the template script of export.sh.tmpl renders.
type scriptView struct {
	// Static tells whether the script builds a static program, for the musl
	// target of the machine of Triples (the template musl).
	Static  bool
	Triples []tripleView
	// Module tells whether the script builds the library of the package as
	// the module, of the arguments of cargo ModuleArgs, DarwinArgs on macOS,
	// as ModuleName, or under the name of the library when it is empty.
	Module                 bool
	ModuleArgs, DarwinArgs []string
	ModuleName             string
	// Bin is the binary the script builds, of the arguments of cargo
	// BinArgs; empty for none.
	Bin     string
	BinArgs []string
	// ProfileDir is the directory cargo builds the profile into, as a word
	// of sh (profileDir).
	ProfileDir string
	// ErrStaticDarwin, ErrUnsupportedTarget, ErrNoRustTarget and
	// ErrNoLockfile are the errors the script fails with, as Run does.
	ErrStaticDarwin, ErrUnsupportedTarget, ErrNoRustTarget, ErrNoLockfile string
}

// tripleView is an architecture of Go, Arch, and the architecture of the
// Rust target triple of it, Triple (cargoArches).
type tripleView struct {
	Arch, Triple string
}

// exportArgs are args, an argv of cargo of Run, but the format of its
// messages, which the script does not read.
func exportArgs(args []string) []string {
	return slices.DeleteFunc(slices.Clone(args), func(arg string) bool {
		return arg == messageFormat
	})
}

// profileDir is the directory of the target directory cargo builds the
// profile of the build into, as a word of sh: debug for dev and test,
// release for release and bench, that of its name for another.
func (b cargoBuild) profileDir() string {
	profile := b.profile()

	if dir, ok := profileDirs[profile]; ok {
		return dir
	}

	return build.ShellQuote(profile)
}
