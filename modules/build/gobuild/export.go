package gobuild

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/tarantool/tt/sdk/build"
)

// This file is the build of go without tt (build.Exporter): a script of sh
// that runs the go build Run runs, the template script of export.sh.tmpl.

// outputArg is the index of the output of go build among the arguments
// goArgs makes: build -trimpath -o <output>.
const outputArg = 3

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

// Export builds the program as Run does, for the machine the script runs
// on: go build of goArgs into "$TT_PROGRAMS_DIR"/<name>, with the jobs of a
// build of no -j, go's own default, and GOOS and GOARCH of the host as go
// names it, GOHOSTOS and GOHOSTARCH, and CGO_ENABLED of the block, which
// win over the environment as those of targetEnv do. A static program is
// refused for macOS, as Preflight refuses it. The script loses nothing of
// Run: go downloads the modules of go.mod as it does under tt with no
// mirror, the options of which the script has none of, and builds from
// vendor/ when the sources of the package have one.
func (b goBuild) Export(*build.ExportContext) (build.Export, error) {
	// A flag Run cannot pass fails the build under tt too.
	args, err := goArgs(b.config, "", b.static, 0, nil)
	if err != nil {
		return build.Export{}, fmt.Errorf("ldflags: %w", err)
	}

	script, err := renderScript("script", scriptView{
		Static: b.static, ErrStaticDarwin: ErrStaticDarwin.Error(), Cgo: cgoEnabled(b.config),
		Args: args, Output: outputArg, Program: b.program,
	})
	if err != nil {
		return build.Export{}, err
	}

	return build.Export{Script: script, Tools: []string{goTool}, Lost: nil}, nil
}

// scriptView is what the template script of export.sh.tmpl renders.
type scriptView struct {
	// Static is static of the block, which the script refuses for macOS
	// with ErrStaticDarwin, as Preflight does.
	Static          bool
	ErrStaticDarwin string
	// Cgo is CGO_ENABLED of the block (cgoEnabled).
	Cgo string
	// Args are the arguments of go build of goArgs, but the one at Output,
	// the output, in place of which the script has Program in
	// TT_PROGRAMS_DIR.
	Args    []string
	Output  int
	Program string
}
