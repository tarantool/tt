package emmylua

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/modules/emmylua/internal/analyzer"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/fetch"
)

// registryFlagUsage is the help of --registry, as tt package gives it.
const registryFlagUsage = "rock server to use instead of the configured ones: " +
	"an http(s) URL or a local directory; repeat to give an ordered list " +
	"(overrides TT_REGISTRIES and [platform].registries)"

// prepareCmd is tt emmylua prepare: the values of its flags.
type prepareCmd struct {
	services sdk.Services
	// version is the EmmyLua release to prepare.
	version string
	// from is a local build of EmmyLua to pack.
	from string
	// rockServer is the rock server the rock is declared from.
	rockServer string
	// noInstall leaves the install of the rocks to tt package build.
	noInstall bool
	// registries are the rock servers to resolve and install with.
	registries []string
}

// newPrepareCmd builds tt emmylua prepare.
func newPrepareCmd(services sdk.Services) *cobra.Command {
	prepare := &prepareCmd{
		services:   services,
		version:    "",
		from:       "",
		rockServer: "",
		noInstall:  false,
		registries: nil,
	}

	cmd := &cobra.Command{
		Use:   "prepare",
		Short: "Add the programs of EmmyLua to the project",
		Long: "Put EmmyLua into the project.\n\n" +
			"The programs of the release for this host - downloaded and checked " +
			"against the sha256 tt holds them to, or those of a local build (--from) " +
			"- are packed as a binary rock into " + analyzer.RegistryDir + ". The " +
			"manifest declares it as\n\n" +
			"  [dev_dependencies]\n" +
			"  " + analyzer.RockName + " = { version = \"<release>\", registry = \"" +
			analyzer.RegistryDir + "\" }\n\n" +
			"the lock is re-resolved and the rocks are installed, so the programs " +
			"are in .rocks/bin. A declaration already there is left as it is.\n\n" +
			"With --rock-server nothing is packed: the dependency names the server, " +
			"which tt emmylua rock packs the rocks of every platform for.\n\n" +
			"The registry is for this host and is not meant to be committed: after a " +
			"checkout, run tt emmylua prepare again. Known releases: " +
			strings.Join(analyzer.Versions(), ", ") + "; " + analyzer.ReleasesEnv +
			" names a JSON file of others.",
		Example: `# A local build of EmmyLua, from its checkout.
  $ cargo build --release -p emmylua_check -p emmylua_ls -p emmylua_doc_cli -p emmylua_formatter
  $ tt emmylua prepare --from ~/src/emmylua-analyzer-rust/target/release

  # The release Tarantool checks its own code with, downloaded.
  $ tt emmylua prepare --version 0.25.1

  # The rocks of a shared rock server.
  $ tt emmylua prepare --rock-server https://example.org/rocks/`,
		Args: cobra.NoArgs,
		RunE: prepare.run,
	}

	flags := cmd.Flags()
	flags.StringVar(&prepare.version, "version", analyzer.DefaultVersion, "EmmyLua release")
	flags.StringVar(&prepare.from, "from", "",
		"pack the programs of a local build of the release in this directory "+
			"(a cargo target/release) instead of downloading them")
	flags.StringVar(&prepare.rockServer, "rock-server", "",
		"declare the rock from this rock server, which tt emmylua rock packs for, "+
			"instead of packing it into "+analyzer.RegistryDir)
	flags.BoolVar(&prepare.noInstall, "no-install", false,
		"do not install the rocks: leave that to tt package build")
	flags.StringArrayVar(&prepare.registries, "registry", nil, registryFlagUsage)

	cmd.MarkFlagsMutuallyExclusive("from", "rock-server")

	return cmd
}

// run prepares the project: it packs the rock, declares the dependency
// through the project of the SDK, printing how the lock moved, and installs
// the rocks unless --no-install says otherwise.
func (c *prepareCmd) run(cmd *cobra.Command, _ []string) error {
	project := c.services.Project()
	streams := c.services.Streams().IO()

	projectDir, err := project.Dir()
	if err != nil {
		return err
	}

	var install func(ctx context.Context) error

	if !c.noInstall {
		install = func(ctx context.Context) error {
			return project.Install(ctx, sdk.InstallOptions{Registries: c.registries})
		}
	}

	report, err := analyzer.Prepare(cmd.Context(), analyzer.Options{
		ProjectDir: projectDir,
		Version:    c.version,
		From:       c.from,
		RockServer: c.rockServer,
		Fetcher:    newFetcher(),
		Declare: func(ctx context.Context, name, constraint, registry string) (bool, error) {
			change, err := project.AddDependency(ctx, sdk.Dependency{
				Name:       name,
				Constraint: constraint,
				Registry:   registry,
				Dev:        true,
				Registries: c.registries,
			})
			if err != nil {
				return false, err
			}

			printMoves(streams.Out, change.Moves)

			return change.Existed, nil
		},
		Install:  install,
		Progress: streams.Err,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(streams.Out, "EmmyLua %s from %s\n", report.Release.Version,
		report.Registry)

	if !report.Installed {
		_, _ = fmt.Fprintln(streams.Out, "next: tt package build")

		return nil
	}

	_, _ = fmt.Fprintln(streams.Out, "next: tt emmylua check")

	return nil
}

// printMoves reports the changes of the lock to out one per line, as
// tt package does: a rock that arrived shows only its new version, one that
// dropped out is marked as such, and one that moved shows both ends.
func printMoves(out io.Writer, moves []sdk.DependencyMove) {
	for _, move := range moves {
		switch {
		case move.From == "":
			_, _ = fmt.Fprintf(out, "  %s %s\n", move.Name, move.To)
		case move.To == "":
			_, _ = fmt.Fprintf(out, "  %s %s -> (dropped)\n", move.Name, move.From)
		default:
			_, _ = fmt.Fprintf(out, "  %s %s -> %s\n", move.Name, move.From, move.To)
		}
	}
}

// newFetcher returns the fetcher of the assets of EmmyLua: the default
// client of sdk/fetch and the cache in <user cache>/tt/emmylua.
func newFetcher() fetch.Fetcher {
	return fetch.Fetcher{
		Client:   fetch.Client{HTTP: nil, Limit: 0},
		CacheDir: analyzer.DefaultCacheDir(),
	}
}

// rockCmd is tt emmylua rock: the values of its flags.
type rockCmd struct {
	services sdk.Services
	// version is the EmmyLua release to pack.
	version string
	// out is the directory the rocks are packed into.
	out string
	// from is a local build of EmmyLua to pack.
	from string
	// arch is the LuaRocks arch of the build from names.
	arch string
}

// newRockCmd builds tt emmylua rock.
func newRockCmd(services sdk.Services) *cobra.Command {
	rock := &rockCmd{services: services, version: "", out: "", from: "", arch: ""}

	cmd := &cobra.Command{
		Use:   "rock",
		Short: "Pack the rocks of a release for a rock server",
		Long: "Pack the " + analyzer.RockName + " rock of every platform of an EmmyLua " +
			"release into --out, with the manifest that makes the directory a rock " +
			"server, the assets checked against the sha256 tt holds the release to. " +
			"With --from, pack the one local build as the rock of --arch (default this " +
			"host's) and keep the rocks already in --out: run it once for the build of " +
			"each platform. Serve the directory, or upload its files, and declare the " +
			"dependency from it with tt emmylua prepare --rock-server.",
		Example: `# The rocks of a release on GitHub.
  $ tt emmylua rock --version 0.25.1 --out dist/rocks

  # Local builds, one per platform.
  $ tt emmylua rock --from target/release --out dist/rocks
  $ tt emmylua rock --from target/aarch64-apple-darwin/release --arch macosx-aarch64 \
      --out dist/rocks`,
		Args: cobra.NoArgs,
		RunE: rock.run,
	}

	flags := cmd.Flags()
	flags.StringVar(&rock.version, "version", analyzer.DefaultVersion, "EmmyLua release")
	flags.StringVar(&rock.out, "out", "", "the directory to pack the rocks into")
	flags.StringVar(&rock.from, "from", "",
		"pack the programs of a local build in this directory instead of downloading them")
	flags.StringVar(&rock.arch, "arch", "",
		"the LuaRocks arch of the build --from names (linux-x86_64, macosx-aarch64; "+
			"default this host's)")

	_ = cmd.MarkFlagRequired("out")

	return cmd
}

// run packs the rocks of a release and prints their paths and the path of
// the manifest of the rock server.
func (c *rockCmd) run(cmd *cobra.Command, _ []string) error {
	if c.arch != "" && c.from == "" {
		return fmt.Errorf("%w: --arch names the build of --from", errUsage)
	}

	streams := c.services.Streams().IO()

	rocks, err := analyzer.PackRocks(cmd.Context(), analyzer.PackOptions{
		Dir:      c.out,
		Version:  c.version,
		From:     c.from,
		Arch:     c.arch,
		Fetcher:  newFetcher(),
		Progress: streams.Err,
	})
	if err != nil {
		return err
	}

	for _, rock := range rocks {
		_, _ = fmt.Fprintln(streams.Out, rock)
	}

	_, _ = fmt.Fprintln(streams.Out, filepath.Join(c.out, "manifest"))

	return nil
}
