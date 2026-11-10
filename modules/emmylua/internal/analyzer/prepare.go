package analyzer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/sdk/fetch"
)

// manifestFileName marks a tt project.
const manifestFileName = "app.manifest.toml"

// RegistryDir is the rock server prepare writes the rock to, relative to the
// project: the one tt wasm prepare writes to, under .tarantool, which belongs
// to the machine rather than the repository, since the rock is the build for
// this host.
const RegistryDir = projectDirName + "/registry"

// Options configure Prepare.
type Options struct {
	// ProjectDir is the project to prepare: the directory of its manifest.
	ProjectDir string
	// Version is the release; empty is DefaultVersion.
	Version string
	// From, when set, is a local build of the release for this host - the
	// directory of its programs - packed instead of the downloaded one.
	From string
	// RockServer, when set, is a rock server that serves the rocks of the
	// release, which tt emmylua rock packs: the dependency is declared from
	// it, and nothing is packed in the project.
	RockServer string
	// Fetcher downloads the assets of the release.
	Fetcher fetch.Fetcher
	// Declare declares the dev dependency rock on the registry and
	// re-resolves the lock. It reports whether the manifest already declared
	// it, in which case the declaration was left alone.
	Declare func(ctx context.Context, name, constraint, registry string) (bool, error)
	// Install installs the locked rocks into .rocks; nil leaves that to the
	// next tt package build.
	Install func(ctx context.Context) error
	// Progress receives one line per step.
	Progress io.Writer
}

// Report is what Prepare did.
type Report struct {
	// Release is what the project was prepared with.
	Release Release
	// Rock is the path of the rock in the project's registry; empty when the
	// rock comes from a rock server.
	Rock string
	// Registry is the registry the dependency is declared from.
	Registry string
	// Declared is false when the manifest already declared the dependency.
	Declared bool
	// Installed is true when the rocks were installed into .rocks.
	Installed bool
}

// Prepare puts EmmyLua into the project:
//
//  1. packs the programs of the release for this host - downloaded and
//     checked against the sha256 tt holds them to, or read from From - as a
//     binary rock into the project's registry (RegistryDir), indexing it;
//  2. declares emmylua = { version = <release>, registry = RegistryDir } in
//     [dev_dependencies] and re-resolves the lock;
//  3. installs the locked rocks, so the programs are in .rocks/bin.
//
// With RockServer, step 1 is skipped and the dependency names the server.
// Every step can be run again: unchanged programs are the same rock byte for
// byte, a declaration is kept.
func Prepare(ctx context.Context, opts Options) (*Report, error) {
	_, err := os.Stat(filepath.Join(opts.ProjectDir, manifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s has no %s (create one with tt new)",
			ErrNoManifest, opts.ProjectDir, manifestFileName)
	}

	if err != nil {
		return nil, fmt.Errorf("reading the project: %w", err)
	}

	version := opts.Version
	if version == "" {
		version = DefaultVersion
	}

	release, err := LookupRelease(version)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Release:   release,
		Rock:      "",
		Registry:  RegistryDir,
		Declared:  false,
		Installed: false,
	}

	if opts.RockServer != "" {
		report.Registry = opts.RockServer
	} else {
		report.Rock, err = packHostRock(ctx, opts, release)
		if err != nil {
			return nil, err
		}
	}

	if opts.Declare != nil {
		existed, err := opts.Declare(ctx, RockName, release.Version, report.Registry)
		if err != nil {
			return nil, fmt.Errorf("declaring the %s dependency: %w", RockName, err)
		}

		report.Declared = !existed

		if existed {
			progressf(opts.Progress, "%s is already declared in %s; left as it is",
				RockName, manifestFileName)
		} else {
			progressf(opts.Progress, "declared %s %s from %s in [dev_dependencies]",
				RockName, release.Version, report.Registry)
		}
	}

	if opts.Install != nil {
		progressf(opts.Progress, "installing the rocks")

		err = opts.Install(ctx)
		if err != nil {
			return nil, err
		}

		report.Installed = true
	}

	return report, nil
}

// packHostRock does step 1 of Prepare and returns the path of the rock.
func packHostRock(ctx context.Context, opts Options, release Release) (string, error) {
	var (
		programs map[Program][]byte
		err      error
	)

	if opts.From != "" {
		progressf(opts.Progress, "reading the build in %s", opts.From)

		programs, err = ReadBuild(opts.From)
	} else {
		var platform Platform

		platform, err = release.HostPlatform()
		if err != nil {
			return "", err
		}

		progressf(opts.Progress, "fetching EmmyLua %s for %s", release.Version, platform.Suffix)

		programs, err = fetchPrograms(ctx, opts.Fetcher, release, platform)
	}

	if err != nil {
		return "", err
	}

	registry := filepath.Join(opts.ProjectDir, filepath.FromSlash(RegistryDir))

	rock, err := BuildRock(registry, release, remote.HostArch(), programs)
	if err != nil {
		return "", err
	}

	err = indexRegistry(ctx, registry)
	if err != nil {
		return "", err
	}

	progressf(opts.Progress, "packed %s into %s", filepath.Base(rock), RegistryDir)

	return rock, nil
}

// progressf writes one line to w, unless it is nil.
func progressf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}

	_, _ = fmt.Fprintf(w, format+"\n", args...)
}
