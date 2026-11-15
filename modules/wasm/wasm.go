// Package wasm is the tt module of the wasm commands: they prepare a
// project for tarantool-wasm (github.com/tarantool/tarantool-wasm-rs), the
// Tarantool module that runs WebAssembly components, add components that
// tarawasm builds, report how far a project is prepared and pack the rocks
// of a release for a rock server. The module checks the .wasm files a
// package lays out too.
//
// The module is built on the tt SDK alone. [New] is its constructor; a
// distribution of tt lists it in the table of modules it hands to the core.
package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/modules/wasm/internal/wasm"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/build"
)

// defaultPython is the interpreter tarawasm is installed with unless
// --python names another.
const defaultPython = "python3"

// wasmToolchainHelp says what tt does about the toolchain of a language.
const wasmToolchainHelp = "The toolchain of a language is put in place for the language " +
	"of each component tarawasm builds, and for --lang. tt installs wasm-tools, wkg " +
	"and, for Python, componentize-py into the project, each checked against the " +
	"sha256 tt holds it to. The other tools - python3, Go and TinyGo, Rust and " +
	"cargo-component, Node and jco, the wasi-sdk and wit-bindgen, .NET - are the " +
	"user's to install: tt checks their versions, says which to install, and installs " +
	"nothing into the system."

// New builds the wasm module: the wasm group with its prepare, new, status
// and rock commands, mounted at the root of tt, and the check of the .wasm
// files of a package (checkWasmFile).
func New(services sdk.Services) sdk.Module {
	return sdk.Module{
		Commands: []sdk.Mount{{Path: "", Cmd: newWasmCmd(services)}},
		Backends: nil,
		FileChecks: []build.FileCheck{
			{Suffix: wasmSuffix, Check: checkWasmFile},
		},
	}
}

// newWasmCmd builds the `tt wasm` command group.
func newWasmCmd(services sdk.Services) *cobra.Command {
	wasmCmd := &cobra.Command{
		Use:   "wasm",
		Short: "Prepare a project for WebAssembly components (experimental)",
		Long: "Commands for tarantool-wasm, the Tarantool module that runs " +
			"WebAssembly components (github.com/tarantool/tarantool-wasm-rs): prepare " +
			"puts the runtime, tarawasm and the toolchains in place, new adds a " +
			"component, status reports what a build lacks, and rock packs the rocks " +
			"of a release for a rock server. They are a stopgap until tt builds the " +
			"components itself: the components are built by tarawasm for now.",
	}

	wasmCmd.AddCommand(newPrepareCmd(services), newNewCmd(services), newStatusCmd(services),
		newRockCmd(services))

	return wasmCmd
}

// prepareCmd is tt wasm prepare: the values of its flags.
type prepareCmd struct {
	services sdk.Services
	// version is the tarantool-wasm release to declare.
	version string
	// python is the interpreter tarawasm is installed with.
	python string
	// tarawasm is what pip installs instead of the pinned tarawasm.
	tarawasm string
	// noTarawasm leaves tarawasm out.
	noTarawasm bool
	// reinstall makes tarawasm's environment anew.
	reinstall bool
	// language is the language whose toolchain is put in place too.
	language string
	// from is a local build of the module to pack as the rock of this host.
	from string
	// rockServer is the rock server the rock is declared from.
	rockServer string
	// registries are the rock servers to resolve with.
	registries []string
}

// newPrepareCmd builds `tt wasm prepare`.
func newPrepareCmd(services sdk.Services) *cobra.Command {
	prepare := &prepareCmd{
		services:   services,
		version:    "",
		python:     "",
		tarawasm:   "",
		noTarawasm: false,
		reinstall:  false,
		language:   "",
		from:       "",
		rockServer: "",
		registries: nil,
	}

	prepareCmd := &cobra.Command{
		Use:   "prepare",
		Short: "Add the wasm runtime, tarawasm and the toolchains to the project",
		Long: "Prepare the project for tarantool-wasm.\n\n" +
			"The manifest declares the binary rock wasm of the release, its native " +
			"module, from the rock server --rock-server names:\n\n" +
			"  wasm = { version = \"<release>\", registry = \"<rock server>\" }\n\n" +
			"and the lock is re-resolved, so the next tt package build installs it " +
			"where require('wasm') finds it. A declaration already there is left as " +
			"it is. The default server is the rock index of " +
			"tarantool-contrib/rocks-repo,\n\n" +
			"  " + wasm.DefaultRockServer + "\n\n" +
			"which serves the rock of every platform with its sha256, so the lock " +
			"pins them all; another may be one tt wasm rock packs. Any version a " +
			"server serves is taken.\n\n" +
			"With --from, a local build of the module is packed as the rock of this " +
			"host into " + wasm.RegistryDir + " instead, and the dependency names " +
			"that directory; --from packs the releases tt knows: " +
			strings.Join(wasm.Versions(), ", ") + ".\n\n" +
			"tarawasm, which builds the components for now, is installed into a " +
			"virtual environment under .tarantool (Python 3.10 or newer) and linked " +
			"as .tarantool/bin/tarawasm. Component builds find programs there by " +
			"name, and .env/activate.sh puts the directory on the shell's PATH.\n\n" +
			wasmToolchainHelp + "\n\n" +
			"The SDK of each component is restored as the component's lock pins it, " +
			"and must be of the release prepared.\n\n" +
			"The registry and .tarantool are for this host and are not meant to be " +
			"committed: after a checkout, run tt wasm prepare again.",
		Example: `# Prepare the package in the current directory for Python components.
  $ tt wasm prepare --lang python
  $ tt wasm new router --lang python

  # After a checkout: the runtime, tarawasm, the toolchains and the SDKs
  # of the components.
  $ tt wasm prepare

  # Add the runtime only: the components are built some other way.
  $ tt wasm prepare --no-tarawasm

  # A local build of the module.
  $ tt wasm prepare --from target/release/libtarantool_wasm_rs.so`,
		Args: cobra.NoArgs,
		RunE: prepare.run,
	}

	flags := prepareCmd.Flags()
	flags.StringVar(&prepare.version, "version", wasm.DefaultVersion,
		"tarantool-wasm release: any the rock server serves, or one tt knows with --from")
	flags.StringVar(&prepare.python, "python", defaultPython, "Python to install tarawasm with")
	flags.StringVar(&prepare.tarawasm, "tarawasm", "",
		"what pip installs instead of the tarawasm tt pins (a version, a URL, a path)")
	flags.BoolVar(&prepare.noTarawasm, "no-tarawasm", false, "do not install tarawasm")
	flags.BoolVar(&prepare.reinstall, "reinstall", false, "install tarawasm anew")
	flags.StringVar(&prepare.language, "lang", "",
		"put the toolchain of a language in place too: "+strings.Join(wasm.Languages(), ", "))
	flags.StringVar(&prepare.from, "from", "",
		"pack this local build of the module into "+wasm.RegistryDir+
			" and declare the rock from there")
	flags.StringVar(&prepare.rockServer, "rock-server", "",
		"the rock server to declare the rock from: a URL or a directory relative "+
			"to the project (default the rock index of tarantool-contrib/rocks-repo)")
	flags.StringArrayVar(&prepare.registries, "registry", nil, sdk.RegistryFlagUsage)

	prepareCmd.MarkFlagsMutuallyExclusive("from", "rock-server")

	return prepareCmd
}

// run prepares the project the directory of the command is in: it declares
// the dependency through the project of the SDK, printing how the lock
// moved, and writes the scripts of .env there.
func (c *prepareCmd) run(cmd *cobra.Command, _ []string) error {
	project := c.services.Project()
	streams := c.services.Streams().IO()

	projectDir, err := project.Root()
	if err != nil {
		return err
	}

	report, err := wasm.Prepare(cmd.Context(), wasm.Options{
		ProjectDir: projectDir,
		Version:    c.version,
		Fetcher:    newFetcher(),
		Tarawasm: wasm.Tarawasm{
			Python: c.python, Source: c.tarawasm, Reinstall: c.reinstall, Output: streams.Err,
		},
		SkipTarawasm: c.noTarawasm,
		Language:     c.language,
		From:         c.from,
		RockServer:   c.rockServer,
		Manifest:     manifestOf(c.services, projectDir),
		Rocks:        c.services.Rocks(),
		Declare: func(ctx context.Context, name, constraint, registry string) (bool, error) {
			// A declaration there is someone's choice of the version and the
			// server, and is kept: the lock is resolved against it.
			if declared(project, name) {
				change, err := project.Resolve(ctx, sdk.ResolveOptions{Registries: c.registries})
				if err != nil {
					return true, err
				}

				printMoves(streams.Out, change.Moves)

				return true, nil
			}

			change, err := project.AddDependency(ctx, sdk.Dependency{
				Name:       name,
				Constraint: constraint,
				Registry:   registry,
				Dev:        false,
				Registries: c.registries,
			})
			if err != nil {
				return false, err
			}

			printMoves(streams.Out, change.Moves)

			return change.Existed, nil
		},
		PrepareEnv: project.PrepareEnvironment,
		Progress:   streams.Err,
	})
	if err != nil {
		return err
	}

	printPrepared(streams.Out, projectDir, report)

	return nil
}

// newCmd is tt wasm new: the values of its flags.
type newCmd struct {
	services sdk.Services
	// language is the language of the component.
	language string
	// dir, world and template are the directory, the WIT world and the
	// template of the component.
	dir      string
	world    string
	template string
	// registries are the rock servers to resolve with.
	registries []string
}

// newNewCmd builds `tt wasm new`.
func newNewCmd(services sdk.Services) *cobra.Command {
	add := &newCmd{
		services: services, language: "", dir: "", world: "", template: "", registries: nil,
	}

	cmd := &cobra.Command{
		Use:   "new NAME",
		Short: "Add a WebAssembly component to the package",
		Long: "Add a WebAssembly component to the package of the project, which tt " +
			"wasm prepare has prepared.\n\n" +
			"tarawasm creates the component in the directory NAME (or --dir), on the " +
			"SDK of the wasm release the package declares, and a .gitignore keeps what " +
			"it generates out of git. The manifest declares the component, built by " +
			"tarawasm all into the namespace of the package:\n\n" +
			"  [components.NAME]\n" +
			"  path = \"NAME\"\n" +
			"  include = [\"dist/NAME.wasm\"]\n\n" +
			"  [components.NAME.build]\n" +
			"  backend = \"shell\"\n" +
			"  command = \"tarawasm\"\n" +
			"  args = [\"all\"]\n" +
			"  incremental = true\n\n" +
			"The WIT world of the component, and the file it builds, are named after " +
			"it unless --world names them; the build is skipped while its sources " +
			"have not changed.\n\n" +
			wasmToolchainHelp,
		Example: `# A component in Python, in the directory router.
  $ tt wasm new router --lang python

  # An HTTP API over a space, served by the component itself.
  $ tt wasm new router --lang python --template http

  # Elsewhere, with a world of its own name.
  $ tt wasm new api --lang rust --dir wasm/api --world api`,
		Args: cobra.ExactArgs(1),
		RunE: add.run,
	}

	flags := cmd.Flags()
	flags.StringVar(&add.language, "lang", "",
		"the language of the component: "+strings.Join(wasm.Languages(), ", "))
	flags.StringVar(&add.dir, "dir", "", "the directory of the component (default NAME)")
	flags.StringVar(&add.world, "world", "", "the name of its WIT world (default app)")
	flags.StringVar(&add.template, "template", "",
		"start from a template: http (an HTTP API over a space) or call (an exported "+
			"function); Python only")
	flags.StringArrayVar(&add.registries, "registry", nil, sdk.RegistryFlagUsage)

	_ = cmd.MarkFlagRequired("lang")

	return cmd
}

// run adds a component to the package the directory of the command is in.
func (c *newCmd) run(cmd *cobra.Command, args []string) error {
	name := args[0]
	project := c.services.Project()
	streams := c.services.Streams().IO()

	projectDir, err := project.Root()
	if err != nil {
		return err
	}

	report, err := wasm.NewComponent(cmd.Context(), wasm.NewOptions{
		ProjectDir: projectDir,
		Name:       name,
		Dir:        c.dir,
		Language:   c.language,
		World:      c.world,
		Template:   c.template,
		Fetcher:    newFetcher(),
		Tarawasm: wasm.Tarawasm{
			Python: defaultPython, Source: "", Reinstall: false, Output: streams.Err,
		},
		Manifest:     manifestOf(c.services, projectDir),
		AddComponent: project.AddComponent,
		Resolve: func(ctx context.Context) error {
			_, err := project.Resolve(ctx, sdk.ResolveOptions{Registries: c.registries})

			return err
		},
		Progress: streams.Err,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(streams.Out, "added component %s in %s\n", name, report.Dir)

	if report.Module == "" {
		_, _ = fmt.Fprintf(streams.Out, `next:
  # write the component: %s/wit/ and its source
  tt package build
  # wasm.load(fio.pathjoin(debug.sourcedir(), %q)) in a module of the package
`, report.Dir, report.Output)

		return nil
	}

	_, _ = fmt.Fprintf(streams.Out, `next:
  # a Lua module of the package, at the root of its namespace, uses it so:
%s
  tt package build
`, indent(report.Module, "    "))

	return nil
}

// newStatusCmd builds `tt wasm status`.
func newStatusCmd(services sdk.Services) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show how far the project is prepared, changing nothing",
		Long: "Show what tt wasm prepare put in place and tt package build needs: the " +
			"wasm rock the package declares and its build for this host, tarawasm, the " +
			"components tarawasm builds with the SDK each pins, and the toolchain of " +
			"their languages. Nothing is installed or changed. Anything missing is " +
			"listed, and the command fails, as a check in CI does.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd.Context(), services)
		},
	}
}

// runStatus reports the status of the project the directory of the command
// is in.
func runStatus(ctx context.Context, services sdk.Services) error {
	projectDir, err := services.Project().Root()
	if err != nil {
		return err
	}

	man, err := manifestOf(services, projectDir)()
	if err != nil {
		return err
	}

	report, err := wasm.Status(ctx, projectDir, man)
	if err != nil {
		return err
	}

	out := services.Streams().IO().Out

	rel := func(path string) string {
		relative, relErr := filepath.Rel(projectDir, path)
		if relErr != nil || path == "" {
			return path
		}

		return relative
	}
	orMissing := func(value string) string {
		if value == "" {
			return "missing"
		}

		return value
	}

	_, _ = fmt.Fprintf(out, "wasm:       %s from %s\n", orMissing(report.Declared),
		orMissing(report.Registry))

	if report.FromServer {
		_, _ = fmt.Fprintf(out, "rock:       from %s\n", report.Registry)
	} else {
		_, _ = fmt.Fprintf(out, "rock:       %s\n", orMissing(rel(report.Rock)))
	}

	_, _ = fmt.Fprintf(out, "tarawasm:   %s\n", orMissing(rel(report.Tarawasm)))

	for _, component := range report.Components {
		sdkState := "SDK " + orMissing(component.SDKVersion)
		if !component.SDKRestored {
			sdkState += ", not restored"
		}

		_, _ = fmt.Fprintf(out, "component:  %s (%s, %s) in %s\n", component.Name,
			component.Language, sdkState, rel(component.Dir))
	}

	for _, language := range report.Languages {
		_, _ = fmt.Fprint(out, wasm.FormatToolchain(language, report.Toolchains[language]))
	}

	if len(report.Problems) > 0 {
		return fmt.Errorf("%w:\n  %s", wasm.ErrNotReady, strings.Join(report.Problems, "\n  "))
	}

	_, _ = fmt.Fprintln(out, "ready")

	return nil
}

// rockCmd is tt wasm rock: the values of its flags.
type rockCmd struct {
	services sdk.Services
	// version is the tarantool-wasm release to pack.
	version string
	// out is the directory the rocks are packed into.
	out string
	// from is a local build of the module to pack.
	from string
	// arch is the LuaRocks arch of the build from names.
	arch string
}

// newRockCmd builds `tt wasm rock`.
func newRockCmd(services sdk.Services) *cobra.Command {
	rock := &rockCmd{services: services, version: "", out: "", from: "", arch: ""}

	rockCmd := &cobra.Command{
		Use:   "rock",
		Short: "Pack the rocks of a release for a rock server",
		Long: "Pack the wasm rock of every platform of a tarantool-wasm release into " +
			"--out, with the LuaRocks manifest and the rock index that make the " +
			"directory a rock server, the index pinning the sha256 of every rock " +
			"in the lock of a project that depends on it. Serve it, " +
			"or upload its files next to the assets of the release, and declare the " +
			"dependency from it with tt wasm prepare --rock-server: then every machine " +
			"gets the build of its own, and an archive packed --without-deps installs " +
			"anywhere. The assets are checked against the sha256 tt holds the release to. " +
			"With --from, pack the one local build of the module as the rock of --arch " +
			"(default this host's) and keep the rocks already in --out: run it once for " +
			"the build of each platform.",
		Example: `# The rocks of 0.1.0, for the assets of the release on GitHub.
  $ tt wasm rock --out dist/rocks
  $ gh release upload v0.1.0 dist/rocks/*
  $ tt wasm prepare --rock-server \
      https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/

  # Local builds, one per platform.
  $ tt wasm rock --from target/release/libtarantool_wasm_rs.so \
      --out dist/rocks
  $ tt wasm rock --arch macosx-aarch64 --out dist/rocks \
      --from target/release/libtarantool_wasm_rs.dylib`,
		Args: cobra.NoArgs,
		RunE: rock.run,
	}

	flags := rockCmd.Flags()
	flags.StringVar(&rock.version, "version", wasm.DefaultVersion, "tarantool-wasm release")
	flags.StringVar(&rock.out, "out", "", "the directory to pack the rocks into")
	flags.StringVar(&rock.from, "from", "",
		"pack this local build of the module instead of downloading the assets")
	flags.StringVar(&rock.arch, "arch", "",
		"the LuaRocks arch of the build --from names (linux-x86_64, macosx-aarch64; "+
			"default this host's)")

	_ = rockCmd.MarkFlagRequired("out")

	return rockCmd
}

// run packs the rocks of a release and prints their paths and the paths of
// the manifest and the rock index of the rock server.
func (c *rockCmd) run(cmd *cobra.Command, _ []string) error {
	streams := c.services.Streams().IO()

	rocks, err := wasm.PackRocks(cmd.Context(), wasm.PackOptions{
		Dir:      c.out,
		Version:  c.version,
		Fetcher:  newFetcher(),
		Rocks:    c.services.Rocks(),
		Progress: streams.Err,
		From:     c.from,
		Arch:     c.arch,
	})
	if err != nil {
		return err
	}

	for _, rock := range rocks {
		_, _ = fmt.Fprintln(streams.Out, rock)
	}

	_, _ = fmt.Fprintln(streams.Out, filepath.Join(c.out, "manifest"))
	_, _ = fmt.Fprintln(streams.Out, filepath.Join(c.out, remote.RocksIndexFile))

	return nil
}

// newFetcher returns the fetcher of the assets of tarantool-wasm and of the
// tools: the cache in <user cache>/tt/wasm and the default HTTP client.
func newFetcher() wasm.Fetcher {
	return wasm.Fetcher{CacheDir: wasm.DefaultCacheDir(), Client: nil}
}

// declared reports whether the manifest of project declares the rock name
// in [dependencies].
func declared(project sdk.Project, name string) bool {
	man, err := project.Manifest()
	if err != nil {
		return false
	}

	_, ok := man.Dependencies[name]

	return ok
}

// manifestOf reads the manifest of the project in projectDir through the
// project of the SDK, a project without one reported as
// wasm.NoManifestError.
func manifestOf(services sdk.Services, projectDir string) wasm.ReadManifest {
	return func() (sdk.Manifest, error) {
		man, err := services.Project().Manifest()
		if errors.Is(err, sdk.ErrNotFound) {
			return sdk.Manifest{}, wasm.NoManifestError(projectDir)
		}

		return man, err
	}
}

// printMoves reports the changes of the lock to out one per line, as
// tt package does (sdk.DependencyMove.String).
func printMoves(out io.Writer, moves []sdk.DependencyMove) {
	for _, move := range moves {
		_, _ = fmt.Fprintf(out, "  %s\n", move)
	}
}

// indent puts prefix in front of every line of text.
func indent(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}

	return strings.Join(lines, "\n")
}

// printPrepared tells out what was prepared and what to do next.
func printPrepared(out io.Writer, projectDir string, report *wasm.Report) {
	rel := func(path string) string {
		relative, err := filepath.Rel(projectDir, path)
		if err != nil {
			return path
		}

		return relative
	}

	_, _ = fmt.Fprintf(out, "prepared for tarantool-wasm %s (%s)\n",
		report.Release.Version, report.Platform.RockArch)

	if report.Rock != "" {
		_, _ = fmt.Fprintf(out, "  rock:       %s\n", rel(report.Rock))
	} else {
		_, _ = fmt.Fprintf(out, "  rock:       from %s\n", report.Registry)
	}

	if report.Tarawasm != nil {
		_, _ = fmt.Fprintf(out, "  tarawasm:   %s\n", rel(report.Tarawasm.Link))
	}

	if len(report.Languages) > 0 {
		_, _ = fmt.Fprintf(out, "  toolchains: %s\n", strings.Join(report.Languages, ", "))
	}

	if len(report.Components) > 0 {
		_, _ = fmt.Fprintf(out, "  SDKs of:    %s\n", strings.Join(report.Components, ", "))
	}

	if report.Tarawasm != nil && len(report.Components) == 0 {
		_, _ = fmt.Fprintf(out, "next:\n  tt wasm new <name> --lang <language>\n")
	}
}
