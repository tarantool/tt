package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/internal/scope"
)

const (
	// manifestFileName marks a tt project.
	manifestFileName = "app.manifest.toml"
	// RegistryDir is the rock server prepare writes the wasm rock of a local
	// build to, relative to the project: under .tarantool, which belongs to
	// the machine rather than the repository, since the rock is the build for
	// this host. The dependency names it so, and tt resolves it against the
	// project.
	RegistryDir = scope.ProjectDirName + "/registry"
	// DefaultRockServer is the rock server prepare declares the wasm rock
	// from unless it is told another or packs a local build: the
	// tt-rocks-index.json of the index branch of tarantool-contrib/rocks-repo,
	// which names the rocks of every platform on the release pages of the
	// repository with their sha256, so the lock pins them all. tt emmylua
	// prepare declares its rock from the same index.
	DefaultRockServer = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/"
)

// Options configure Prepare.
type Options struct {
	// ProjectDir is the project to prepare: the directory of its manifest.
	ProjectDir string
	// Version is the tarantool-wasm release; empty is DefaultVersion.
	Version string
	// Fetcher downloads the tools of the toolchains.
	Fetcher Fetcher
	// Tarawasm installs tarawasm; SkipTarawasm leaves it out.
	Tarawasm     Tarawasm
	SkipTarawasm bool
	// Language, when set, has tarawasm set up the toolchain of that language.
	Language string
	// From, when set, is a local build of the native module of the release
	// for this host, packed into the project's registry (RegistryDir), and
	// the dependency is declared from there. The release must be one tt
	// knows (LookupRelease): the rockspec is written from it.
	From string
	// RockServer is the rock server the dependency is declared from when
	// From is not set, which serves the rocks of the release: one with a
	// tt-rocks-index.json or one tt wasm rock packs. Nothing is packed in the
	// project, and any version is taken: the server, not tt, knows which it
	// serves. Empty is DefaultRockServer.
	RockServer string
	// Declare declares the dependency rock on the registry and re-resolves the
	// lock. It reports whether the manifest already declared it, in which case
	// the declaration was left alone.
	Declare func(ctx context.Context, name, constraint, registry string) (bool, error)
	// PrepareEnv writes the project's .env/activate.sh, when it is missing.
	PrepareEnv func() error
	// Progress receives one line per step.
	Progress io.Writer
}

// Report is what Prepare did.
type Report struct {
	// Release and Platform are what the project was prepared with: for a
	// local build, the release tt knows and the platform it was packed as;
	// for a rock server, only the version of the release and the LuaRocks
	// arch of this host.
	Release  Release
	Platform Platform
	// Rock is the path of the rock in the project's registry; empty when the
	// rock comes from RockServer.
	Rock string
	// Registry is the registry the dependency is declared from.
	Registry string
	// Declared is false when the manifest already declared the dependency.
	Declared bool
	// Tarawasm is where tarawasm went; nil when it was skipped.
	Tarawasm *TarawasmLayout
	// TarawasmInstalled is false when an installed tarawasm was kept.
	TarawasmInstalled bool
	// Languages are the languages whose toolchains were checked.
	Languages []string
	// Components are the components whose SDK was restored.
	Components []string
}

// Prepare prepares the project for tarantool-wasm:
//
//  1. with From, packs the local build of the native module as the binary
//     rock of the release for this host into the project's registry
//     (RegistryDir), indexing the registry;
//  2. declares wasm = { version = <release>, registry = <server> } and
//     re-resolves the lock, so the next tt package build installs it into
//     .rocks/lib/tarantool, where require('wasm') finds it: the server is
//     RegistryDir after step 1, and RockServer - DefaultRockServer, unless
//     told otherwise - without From;
//  3. installs tarawasm into .tarantool and links it as .tarantool/bin/tarawasm,
//     which component builds find by name;
//  4. checks that the components tarawasm builds use the SDK of the release;
//  5. for the language of each such component, and Language, installs the
//     tools tt installs into .tarantool, checks the tools the user installs
//     and selects the toolchain for tarawasm (see EnsureToolchain);
//  6. restores the SDK of each such component, which a checkout lacks;
//  7. writes .env/activate.sh, which puts .tarantool/bin on the shell's PATH.
//
// Every step can be run again: an unchanged rock is rewritten byte for byte,
// a declaration is kept, an installed tarawasm is relinked.
func Prepare(ctx context.Context, opts Options) (*Report, error) {
	_, err := os.Stat(filepath.Join(opts.ProjectDir, manifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s has no %s (create one with tt new)",
			ErrNoManifest, opts.ProjectDir, manifestFileName)
	}

	if err != nil {
		return nil, fmt.Errorf("reading the project: %w", err)
	}

	release, platform, err := preparedRelease(opts)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Release:           release,
		Platform:          platform,
		Rock:              "",
		Registry:          opts.RockServer,
		Declared:          false,
		Tarawasm:          nil,
		TarawasmInstalled: false,
		Languages:         nil,
		Components:        nil,
	}

	if opts.SkipTarawasm && opts.Language != "" {
		return nil, fmt.Errorf("%w: the toolchain of --lang is tarawasm's; drop --no-tarawasm",
			ErrNoTarawasm)
	}

	err = prepareRock(ctx, opts, report)
	if err != nil {
		return nil, err
	}

	if !opts.SkipTarawasm {
		err = prepareTarawasm(ctx, opts, report)
		if err != nil {
			return nil, err
		}
	}

	if opts.PrepareEnv != nil {
		err = opts.PrepareEnv()
		if err != nil {
			return nil, fmt.Errorf("writing .env/activate.sh: %w", err)
		}
	}

	return report, nil
}

// preparedRelease returns the release and the platform Prepare prepares the
// project for. A local build is packed from the release tt knows, as the rock
// of this host; a rock server knows its own releases, so any version is
// taken, and the arch of this host is all there is to say of the platform.
func preparedRelease(opts Options) (Release, Platform, error) {
	version := opts.Version
	if version == "" {
		version = DefaultVersion
	}

	if opts.From == "" {
		release := Release{
			Version: version, RockRevision: 0, SDKAsset: "", SDKMD5: "", Platforms: nil,
			BaseURL: "",
		}
		platform := Platform{
			GOOS: "", GOARCH: "", Asset: "", SHA256: "", RockArch: hostRockArch(), Module: "",
		}

		return release, platform, nil
	}

	release, err := LookupRelease(version)
	if err != nil {
		return Release{}, Platform{}, err
	}

	platform, err := LocalPlatform("")
	if err != nil {
		return Release{}, Platform{}, err
	}

	return release, platform, nil
}

// prepareRock does steps 1 and 2 of Prepare.
func prepareRock(ctx context.Context, opts Options, report *Report) error {
	switch {
	case opts.From != "":
		err := packLocalBuild(ctx, opts, report)
		if err != nil {
			return err
		}
	case report.Registry == "":
		report.Registry = DefaultRockServer
	}

	if opts.Declare == nil {
		return nil
	}

	existed, err := opts.Declare(ctx, RockName, report.Release.Version, report.Registry)
	if err != nil {
		return fmt.Errorf("declaring the %s dependency: %w", RockName, err)
	}

	report.Declared = !existed

	if existed {
		progressf(opts, "%s is already declared in %s; left as it is", RockName, manifestFileName)
	} else {
		progressf(opts, "declared %s %s from %s", RockName, report.Release.Version,
			report.Registry)
	}

	return nil
}

// packLocalBuild does step 1 of Prepare: it packs the module opts.From names
// as the rock of the release and the platform of report into the project's
// registry, indexes it, and has report declare the rock from there.
func packLocalBuild(ctx context.Context, opts Options, report *Report) error {
	progressf(opts, "reading the build in %s", opts.From)

	module, err := os.ReadFile(opts.From)
	if err != nil {
		return fmt.Errorf("reading the build: %w", err)
	}

	registry := filepath.Join(opts.ProjectDir, filepath.FromSlash(RegistryDir))

	rock, err := BuildRock(registry, report.Release, report.Platform, module)
	if err != nil {
		return err
	}

	err = IndexRegistry(ctx, registry)
	if err != nil {
		return err
	}

	report.Rock = rock
	report.Registry = RegistryDir

	progressf(opts, "packed %s into %s", filepath.Base(rock), RegistryDir)

	return nil
}

// prepareTarawasm does steps 3 to 6 of Prepare.
func prepareTarawasm(ctx context.Context, opts Options, report *Report) error {
	root := filepath.Join(opts.ProjectDir, scope.ProjectDirName)

	progressf(opts, "installing tarawasm")

	layout, installed, err := opts.Tarawasm.Install(ctx, root)
	if err != nil {
		return err
	}

	report.Tarawasm = &layout
	report.TarawasmInstalled = installed

	components, err := FindComponents(opts.ProjectDir)
	if err != nil {
		return err
	}

	mismatches := SDKMismatches(components, report.Release.Version)
	if len(mismatches) > 0 {
		return fmt.Errorf("%w:\n  %s\nprepare the release of the components with "+
			"--version, or move the components to the SDK of %s as tarawasm describes",
			ErrSDKVersion, strings.Join(mismatches, "\n  "), report.Release.Version)
	}

	unlinked := map[string]map[string]string{}

	for _, language := range componentLanguages(components, opts.Language) {
		unlinked[language], err = ensureLanguage(ctx, opts, root, layout, language)
		if err != nil {
			return err
		}

		report.Languages = append(report.Languages, language)
	}

	for _, component := range components {
		if component.SDKVersion == "" {
			continue
		}

		err = opts.Tarawasm.configureComponent(ctx, layout.Link, component,
			unlinked[component.Language])
		if err != nil {
			return err
		}

		progressf(opts, "restoring the SDK of component %s", component.Name)

		err = opts.Tarawasm.restoreSDK(ctx, layout.Link, component)
		if err != nil {
			return err
		}

		report.Components = append(report.Components, component.Name)
	}

	return nil
}

// ensureLanguage puts the toolchain of language in place, as far as tt
// does, checks the rest and selects it for tarawasm. It returns the programs
// it installed but did not link, which each component of the language is
// told of.
func ensureLanguage(
	ctx context.Context, opts Options, root string, layout TarawasmLayout, language string,
) (map[string]string, error) {
	progressf(opts, "checking the %s toolchain", language)

	result, err := EnsureToolchain(ctx, ToolchainOptions{
		Root:        root,
		Language:    language,
		Fetcher:     opts.Fetcher,
		TarawasmEnv: layout.Env,
		Output:      opts.Tarawasm.Output,
	})
	if result != nil && opts.Progress != nil {
		_, _ = fmt.Fprint(opts.Progress, FormatToolchain(language, result.Statuses))
	}

	if err != nil {
		return nil, err
	}

	err = opts.Tarawasm.UseToolchain(ctx, layout.Link, language, result.Unlinked)
	if err != nil {
		return nil, err
	}

	return result.Unlinked, nil
}

// componentLanguages returns the languages of the components and extra, each
// once, in order.
func componentLanguages(components []Component, extra string) []string {
	seen := map[string]bool{}

	if extra != "" {
		seen[extra] = true
	}

	for _, component := range components {
		if component.Language != "" {
			seen[component.Language] = true
		}
	}

	return sortedKeys(seen)
}

// progressf writes one line to opts.Progress.
func progressf(opts Options, format string, args ...any) {
	if opts.Progress == nil {
		return
	}

	_, _ = fmt.Fprintf(opts.Progress, format+"\n", args...)
}
