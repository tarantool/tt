package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/internal/scope"
)

var (
	// componentNameRe is what a component may be named: a bare TOML key.
	componentNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// witNameRe is a WIT identifier: lowercase words joined by dashes.
	witNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z][a-z0-9]*)*$`)

	// errBadName reports a component name that cannot be a bare TOML key.
	errBadName = errors.New("bad component name")
)

// NewOptions configure NewComponent.
type NewOptions struct {
	// ProjectDir is the package the component is added to.
	ProjectDir string
	// Name is the component's name in the manifest.
	Name string
	// Dir is its directory, relative to the project; empty is Name.
	Dir string
	// Language is the language of the component.
	Language string
	// World names its WIT world, and so the component it builds:
	// dist/<world>.wasm. Empty is the name of the component.
	World string
	// Template, when set, is the template laid over the component.
	Template string
	// Fetcher downloads the tools tt installs.
	Fetcher Fetcher
	// Tarawasm runs the tarawasm of the project.
	Tarawasm Tarawasm
	// Resolve re-resolves the lock after the manifest changed.
	Resolve func(ctx context.Context) error
	// Progress receives one line per step.
	Progress io.Writer
}

// NewReport is what NewComponent did.
type NewReport struct {
	// Dir is the component's directory, relative to the project.
	Dir string
	// Output is the component it builds, relative to Dir.
	Output string
	// Module is the Lua module the template uses the component with; empty
	// without a template.
	Module string
}

// NewComponent adds a WebAssembly component to the package:
//
//  1. puts the toolchain of the language in place, as Prepare does;
//  2. has tarawasm create the project of the component, on the SDK of the
//     wasm release the package declares, leaving the module to the rock;
//  3. writes its .gitignore, of what tarawasm generates;
//  4. declares it in the manifest, built by tarawasm all, and re-resolves the
//     lock.
//
// The project has to have been prepared: tarawasm comes from tt wasm prepare.
func NewComponent(ctx context.Context, opts NewOptions) (*NewReport, error) {
	if !componentNameRe.MatchString(opts.Name) {
		return nil, fmt.Errorf("%w: %q: letters, digits, - and _ only", errBadName, opts.Name)
	}

	if opts.World == "" {
		if !witNameRe.MatchString(opts.Name) {
			return nil, fmt.Errorf("%w: %q is no WIT name, which names the world of the "+
				"component: give --world", errBadName, opts.Name)
		}

		opts.World = opts.Name
	}

	if opts.Template != "" && !slices.Contains(Templates(opts.Language), opts.Template) {
		return nil, fmt.Errorf("%w %q for %s; there are: %s", errUnknownTemplate, opts.Template,
			opts.Language, strings.Join(Templates(opts.Language), ", "))
	}

	man, err := readManifest(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	if _, ok := man.Components[opts.Name]; ok {
		return nil, fmt.Errorf("%w: %s", ErrComponentExists, opts.Name)
	}

	version, err := DeclaredVersion(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	if version == "" {
		return nil, fmt.Errorf("%w: the package declares no exact %s version; "+
			"run tt wasm prepare", ErrNoTarawasm, RockName)
	}

	root := filepath.Join(opts.ProjectDir, scope.ProjectDirName)
	layout := tarawasmLayout(root, opts.Tarawasm.Source)

	if !isExecutable(layout.Link) {
		return nil, fmt.Errorf("%w: %s is missing; run tt wasm prepare", ErrNoTarawasm,
			layout.Link)
	}

	prepare := Options{
		ProjectDir: opts.ProjectDir, Version: version, Fetcher: opts.Fetcher,
		Tarawasm: opts.Tarawasm, SkipTarawasm: false, Language: opts.Language,
		RockServer: "", Declare: nil, PrepareEnv: nil, Progress: opts.Progress,
	}

	unlinked, err := ensureLanguage(ctx, prepare, root, layout, opts.Language)
	if err != nil {
		return nil, err
	}

	dir := opts.Dir
	if dir == "" {
		dir = opts.Name
	}

	report, err := initComponent(ctx, opts, layout.Link, dir, version)
	if err != nil {
		return nil, err
	}

	component := Component{
		Name: opts.Name, Dir: filepath.Join(opts.ProjectDir, dir), Language: opts.Language,
		Output: report.Output, SDKVersion: version,
	}

	err = opts.Tarawasm.configureComponent(ctx, layout.Link, component, unlinked)
	if err != nil {
		return nil, err
	}

	if opts.Template != "" {
		report.Module, err = applyTemplate(component.Dir, opts.Language, opts.Template,
			&component)
		if err != nil {
			return nil, err
		}
	}

	err = declareComponent(opts.ProjectDir, opts.Name, dir, report.Output)
	if err != nil {
		return nil, err
	}

	progressf(prepare, "declared component %s in %s", opts.Name, manifestFileName)

	if opts.Resolve != nil {
		err = opts.Resolve(ctx)
		if err != nil {
			return nil, fmt.Errorf("re-resolving the lock: %w", err)
		}
	}

	return report, nil
}

// initComponent has tarawasm create the project of the component in dir and
// writes its .gitignore.
func initComponent(
	ctx context.Context, opts NewOptions, program, dir, version string,
) (*NewReport, error) {
	args := []string{
		"init", "--target", "tarantool", "--sdk-only", "--lang", opts.Language,
		"--tarantool-wasm-version", version, "--world", opts.World, dir,
	}

	runner := opts.Tarawasm

	runner.binDir = filepath.Dir(program)

	err := runner.runIn(ctx, opts.ProjectDir, program, args...)
	if err != nil {
		return nil, fmt.Errorf("creating the component: %w", err)
	}

	component, err := readComponent(opts.Name, filepath.Join(opts.ProjectDir, dir))
	if err != nil {
		return nil, err
	}

	if component == nil || component.Output == "" {
		return nil, fmt.Errorf("%w: tarawasm wrote no %s with an output in %s",
			ErrNoTarawasm, tarawasmConfig, dir)
	}

	ignore := filepath.Join(opts.ProjectDir, dir, ".gitignore")

	_, err = os.Stat(ignore)
	if errors.Is(err, fs.ErrNotExist) {
		err = writeFileAtomic(ignore, []byte(gitignore(opts.Language)))
	}

	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", ignore, err)
	}

	return &NewReport{Dir: dir, Output: component.Output, Module: ""}, nil
}

// declareComponent appends the component to the manifest, after checking that
// the manifest it makes still parses and validates.
func declareComponent(projectDir, name, dir, output string) error {
	path := filepath.Join(projectDir, manifestFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	block := fmt.Sprintf(`
# A WebAssembly component, made by tt wasm new and built by tarawasm.
[components.%[1]s]
path = %[2]s
include = [%[3]s]

[components.%[1]s.build]
backend = "shell"
command = "tarawasm"
args = ["all"]
incremental = true
`, name, strconv.Quote(filepath.ToSlash(dir)), strconv.Quote(output))

	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	edited := slices.Concat(data, []byte(block))

	man, _, err := manifest.ParseManifest(edited)
	if err != nil {
		return fmt.Errorf("the manifest with component %s does not parse: %w", name, err)
	}

	_, err = man.Validate()
	if err != nil {
		return fmt.Errorf("the manifest with component %s is invalid: %w", name, err)
	}

	return writeFileAtomic(path, edited)
}
