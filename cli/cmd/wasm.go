package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/manifest/deps"
	"github.com/tarantool/tt/v3/cli/manifest/rockindex"
	"github.com/tarantool/tt/v3/cli/wasm"
	"github.com/tarantool/tt/v3/internal/binary/install"
)

var (
	// wasmVersion is the tarantool-wasm release tt wasm prepare declares and
	// tt wasm rock packs.
	wasmVersion string
	// wasmPython is the interpreter tarawasm is installed with.
	wasmPython string
	// wasmTarawasm is what pip installs instead of the pinned tarawasm.
	wasmTarawasm string
	// wasmNoTarawasm leaves tarawasm out.
	wasmNoTarawasm bool
	// wasmReinstall makes tarawasm's environment anew.
	wasmReinstall bool
	// wasmLanguage is the language whose toolchain is put in place.
	wasmLanguage string
	// wasmDir, wasmWorld and wasmTemplate are the directory, the world and
	// the template of tt wasm new.
	wasmDir      string
	wasmWorld    string
	wasmTemplate string
	// wasmRockServer is the rock server tt wasm prepare declares the rock
	// from; wasmOut is the directory tt wasm rock packs into.
	wasmRockServer string
	wasmOut        string
	// wasmFrom is a local build of the module tt wasm prepare packs as the
	// rock of this host, and tt wasm rock as the rock of wasmArch.
	wasmFrom string
	wasmArch string
)

// wasmToolchainHelp says what tt does about the toolchain of a language.
const wasmToolchainHelp = "The toolchain of a language is put in place for the language " +
	"of each component tarawasm builds, and for --lang. tt installs wasm-tools, wkg " +
	"and, for Python, componentize-py into the project, each checked against the " +
	"sha256 tt holds it to. The other tools - python3, Go and TinyGo, Rust and " +
	"cargo-component, Node and jco, the wasi-sdk and wit-bindgen, .NET - are the " +
	"user's to install: tt checks their versions, says which to install, and installs " +
	"nothing into the system."

// NewWasmCmd creates the `tt wasm` command group.
func NewWasmCmd() *cobra.Command {
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

	wasmCmd.AddCommand(newWasmPrepareCmd(), newWasmNewCmd(), newWasmStatusCmd(),
		newWasmRockCmd())

	return wasmCmd
}

// newWasmPrepareCmd wires `tt wasm prepare`.
func newWasmPrepareCmd() *cobra.Command {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runWasmPrepare())
		},
	}

	flags := prepareCmd.Flags()
	flags.StringVar(&wasmVersion, "version", wasm.DefaultVersion,
		"tarantool-wasm release: any the rock server serves, or one tt knows with --from")
	flags.StringVar(&wasmPython, "python", "python3", "Python to install tarawasm with")
	flags.StringVar(&wasmTarawasm, "tarawasm", "",
		"what pip installs instead of the tarawasm tt pins (a version, a URL, a path)")
	flags.BoolVar(&wasmNoTarawasm, "no-tarawasm", false, "do not install tarawasm")
	flags.BoolVar(&wasmReinstall, "reinstall", false, "install tarawasm anew")
	flags.StringVar(&wasmLanguage, "lang", "",
		"put the toolchain of a language in place too: "+strings.Join(wasm.Languages(), ", "))
	flags.StringVar(&wasmFrom, "from", "",
		"pack this local build of the module into "+wasm.RegistryDir+
			" and declare the rock from there")
	flags.StringVar(&wasmRockServer, "rock-server", "",
		"the rock server to declare the rock from: a URL or a directory relative "+
			"to the project (default the rock index of tarantool-contrib/rocks-repo)")

	prepareCmd.MarkFlagsMutuallyExclusive("from", "rock-server")

	addRegistryFlag(prepareCmd)

	return prepareCmd
}

// newWasmNewCmd wires `tt wasm new`.
func newWasmNewCmd() *cobra.Command {
	newCmd := &cobra.Command{
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runWasmNew(args[0]))
		},
	}

	flags := newCmd.Flags()
	flags.StringVar(&wasmLanguage, "lang", "",
		"the language of the component: "+strings.Join(wasm.Languages(), ", "))
	flags.StringVar(&wasmDir, "dir", "", "the directory of the component (default NAME)")
	flags.StringVar(&wasmWorld, "world", "", "the name of its WIT world (default app)")
	flags.StringVar(&wasmTemplate, "template", "",
		"start from a template: http (an HTTP API over a space) or call (an exported "+
			"function); Python only")

	_ = newCmd.MarkFlagRequired("lang")

	addRegistryFlag(newCmd)

	return newCmd
}

// newWasmStatusCmd wires `tt wasm status`.
func newWasmStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   statusCmdName,
		Short: "Show how far the project is prepared, changing nothing",
		Long: "Show what tt wasm prepare put in place and tt package build needs: the " +
			"wasm rock the package declares and its build for this host, tarawasm, the " +
			"components tarawasm builds with the SDK each pins, and the toolchain of " +
			"their languages. Nothing is installed or changed. Anything missing is " +
			"listed, and the command fails, as a check in CI does.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runWasmStatus())
		},
	}
}

// runWasmStatus reports the status of the project the working directory is
// in.
func runWasmStatus() error {
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	report, err := wasm.Status(context.Background(), projectDir)
	if err != nil {
		return err
	}

	rel := func(path string) string {
		out, relErr := filepath.Rel(projectDir, path)
		if relErr != nil || path == "" {
			return path
		}

		return out
	}
	orMissing := func(value string) string {
		if value == "" {
			return "missing"
		}

		return value
	}

	_, _ = fmt.Fprintf(os.Stdout, "wasm:       %s from %s\n", orMissing(report.Declared),
		orMissing(report.Registry))

	if report.FromServer {
		_, _ = fmt.Fprintf(os.Stdout, "rock:       from %s\n", report.Registry)
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "rock:       %s\n", orMissing(rel(report.Rock)))
	}

	_, _ = fmt.Fprintf(os.Stdout, "tarawasm:   %s\n", orMissing(rel(report.Tarawasm)))

	for _, component := range report.Components {
		sdk := "SDK " + orMissing(component.SDKVersion)
		if !component.SDKRestored {
			sdk += ", not restored"
		}

		_, _ = fmt.Fprintf(os.Stdout, "component:  %s (%s, %s) in %s\n", component.Name,
			component.Language, sdk, rel(component.Dir))
	}

	for _, language := range report.Languages {
		_, _ = fmt.Fprint(os.Stdout, wasm.FormatToolchain(language, report.Toolchains[language]))
	}

	if len(report.Problems) > 0 {
		return fmt.Errorf("%w:\n  %s", wasm.ErrNotReady, strings.Join(report.Problems, "\n  "))
	}

	_, _ = fmt.Fprintln(os.Stdout, "ready")

	return nil
}

// newWasmRockCmd wires `tt wasm rock`.
func newWasmRockCmd() *cobra.Command {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runWasmRock())
		},
	}

	flags := rockCmd.Flags()
	flags.StringVar(&wasmVersion, "version", wasm.DefaultVersion, "tarantool-wasm release")
	flags.StringVar(&wasmOut, "out", "", "the directory to pack the rocks into")
	flags.StringVar(&wasmFrom, "from", "",
		"pack this local build of the module instead of downloading the assets")
	flags.StringVar(&wasmArch, "arch", "",
		"the LuaRocks arch of the build --from names (linux-x86_64, macosx-aarch64; "+
			"default this host's)")

	_ = rockCmd.MarkFlagRequired("out")

	return rockCmd
}

// runWasmRock packs the rocks of a release.
func runWasmRock() error {
	rocks, err := wasm.PackRocks(context.Background(), wasm.PackOptions{
		Dir:      wasmOut,
		Version:  wasmVersion,
		Fetcher:  wasm.Fetcher{CacheDir: wasm.DefaultCacheDir(), Client: nil},
		Progress: os.Stderr,
		From:     wasmFrom,
		Arch:     wasmArch,
	})
	if err != nil {
		return err
	}

	for _, rock := range rocks {
		_, _ = fmt.Fprintln(os.Stdout, rock)
	}

	_, _ = fmt.Fprintln(os.Stdout, filepath.Join(wasmOut, "manifest"))
	_, _ = fmt.Fprintln(os.Stdout, filepath.Join(wasmOut, rockindex.File))

	return nil
}

// wasmTarawasmRunner is the tarawasm of the project, as the flags configure it.
func wasmTarawasmRunner() wasm.Tarawasm {
	return wasm.Tarawasm{
		Python:    wasmPython,
		Source:    wasmTarawasm,
		Reinstall: wasmReinstall,
		Output:    os.Stderr,
	}
}

// runWasmPrepare prepares the project the working directory is in.
func runWasmPrepare() error {
	opts, err := dependencyOptions()
	if err != nil {
		return err
	}

	report, err := wasm.Prepare(context.Background(), wasm.Options{
		ProjectDir:   opts.ProjectDir,
		Version:      wasmVersion,
		Fetcher:      wasm.Fetcher{CacheDir: wasm.DefaultCacheDir(), Client: nil},
		Tarawasm:     wasmTarawasmRunner(),
		SkipTarawasm: wasmNoTarawasm,
		Language:     wasmLanguage,
		From:         wasmFrom,
		RockServer:   wasmRockServer,
		Declare: func(ctx context.Context, name, constraint, registry string) (bool, error) {
			result, err := deps.AddFromRegistry(ctx, opts, name, constraint, registry, false)
			if err != nil {
				return false, err
			}

			printMoves(result.Moves)

			return result.Change.Existed, nil
		},
		PrepareEnv: install.PrepareProjectEnvironment,
		Progress:   os.Stderr,
	})
	if err != nil {
		return err
	}

	printWasmPrepared(opts.ProjectDir, report)

	return nil
}

// runWasmNew adds a component to the package the working directory is in.
func runWasmNew(name string) error {
	opts, err := dependencyOptions()
	if err != nil {
		return err
	}

	report, err := wasm.NewComponent(context.Background(), wasm.NewOptions{
		ProjectDir: opts.ProjectDir,
		Name:       name,
		Dir:        wasmDir,
		Language:   wasmLanguage,
		World:      wasmWorld,
		Template:   wasmTemplate,
		Fetcher:    wasm.Fetcher{CacheDir: wasm.DefaultCacheDir(), Client: nil},
		Tarawasm:   wasmTarawasmRunner(),
		Resolve: func(ctx context.Context) error {
			_, err := deps.Resolve(ctx, opts)

			return err
		},
		Progress: os.Stderr,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "added component %s in %s\n", name, report.Dir)

	if report.Module == "" {
		_, _ = fmt.Fprintf(os.Stdout, `next:
  # write the component: %s/wit/ and its source
  tt package build
  # wasm.load(fio.pathjoin(debug.sourcedir(), %q)) in a module of the package
`, report.Dir, report.Output)

		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout, `next:
  # a Lua module of the package, at the root of its namespace, uses it so:
%s
  tt package build
`, indent(report.Module, "    "))

	return nil
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

// printWasmPrepared tells what was prepared and what to do next.
func printWasmPrepared(projectDir string, report *wasm.Report) {
	rel := func(path string) string {
		out, err := filepath.Rel(projectDir, path)
		if err != nil {
			return path
		}

		return out
	}

	_, _ = fmt.Fprintf(os.Stdout, "prepared for tarantool-wasm %s (%s)\n",
		report.Release.Version, report.Platform.RockArch)
	if report.Rock != "" {
		_, _ = fmt.Fprintf(os.Stdout, "  rock:       %s\n", rel(report.Rock))
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "  rock:       from %s\n", report.Registry)
	}

	if report.Tarawasm != nil {
		_, _ = fmt.Fprintf(os.Stdout, "  tarawasm:   %s\n", rel(report.Tarawasm.Link))
	}

	if len(report.Languages) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "  toolchains: %s\n", strings.Join(report.Languages, ", "))
	}

	if len(report.Components) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "  SDKs of:    %s\n", strings.Join(report.Components, ", "))
	}

	if report.Tarawasm != nil && len(report.Components) == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "next:\n  tt wasm new <name> --lang <language>\n")
	}
}

// warnWasmSDK warns of the components of the package in projectDir whose SDK
// is of another release than the wasm rock the package declares.
func warnWasmSDK(projectDir string) {
	for _, warning := range wasm.SDKWarnings(projectDir) {
		log.Warn(warning)
	}
}
