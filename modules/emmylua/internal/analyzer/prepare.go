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
)

// manifestFileName marks a tt project.
const manifestFileName = "app.manifest.toml"

// RegistryDir is the rock server prepare writes the rock to, relative to the
// project: the one tt wasm prepare writes to, under .tarantool, which belongs
// to the machine rather than the repository, since the rock is the build for
// this host.
const RegistryDir = projectDirName + "/registry"

// DefaultRockServer is the rock server prepare declares the rock from unless
// it is told another or packs a local build: the tt-rocks-index.json of the
// index branch of tarantool-contrib/rocks-repo, which names the rocks of every
// platform on the release pages of the repository with their sha256, so the
// lock pins them all. tt wasm prepare declares its rock from the same index.
const DefaultRockServer = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/"

// Options configure Prepare.
type Options struct {
	// ProjectDir is the project to prepare: the directory of its manifest.
	ProjectDir string
	// Version is the release; empty is DefaultVersion.
	Version string
	// From, when set, is a local build of the release for this host - the
	// directory of its programs - packed into the project's registry, and
	// the dependency is declared from there. The release must be one tt
	// knows (LookupRelease): the rockspec is written from it.
	From string
	// RockServer is the rock server the dependency is declared from when
	// From is not set, which serves the rocks of the release: one with a
	// tt-rocks-index.json or one tt emmylua rock packs. Nothing is packed in
	// the project, and any version is taken: the server, not tt, knows
	// which it serves. Empty is DefaultRockServer.
	RockServer string
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
	// Release is what the project was prepared with: the one tt knows for
	// a local build, and only its Version for a rock server.
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
//  1. with From, packs the programs of the local build as the binary rock of
//     the release for this host into the project's registry (RegistryDir),
//     indexing it;
//  2. declares emmylua = { version = <release>, registry = <server> } in
//     [dev_dependencies] and re-resolves the lock: the server is RegistryDir
//     after step 1, and RockServer - DefaultRockServer, unless told
//     otherwise - without From;
//  3. installs the locked rocks, so the programs are in .rocks/bin.
//
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

	report := &Report{
		Release: Release{
			Version: version, RockRevision: 0, Ref: "", SourceMD5: "", Platforms: nil, BaseURL: "",
		},
		Rock:      "",
		Registry:  opts.RockServer,
		Declared:  false,
		Installed: false,
	}

	if opts.From != "" {
		report.Release, err = LookupRelease(version)
		if err != nil {
			return nil, err
		}

		report.Registry = RegistryDir

		report.Rock, err = packHostRock(ctx, opts, report.Release)
		if err != nil {
			return nil, err
		}
	} else if report.Registry == "" {
		report.Registry = DefaultRockServer
	}

	if opts.Declare != nil {
		existed, err := opts.Declare(ctx, RockName, report.Release.Version, report.Registry)
		if err != nil {
			return nil, fmt.Errorf("declaring the %s dependency: %w", RockName, err)
		}

		report.Declared = !existed

		if existed {
			progressf(opts.Progress, "%s is already declared in %s; left as it is",
				RockName, manifestFileName)
		} else {
			progressf(opts.Progress, "declared %s %s from %s in [dev_dependencies]",
				RockName, report.Release.Version, report.Registry)
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
	progressf(opts.Progress, "reading the build in %s", opts.From)

	programs, err := ReadBuild(opts.From)
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
