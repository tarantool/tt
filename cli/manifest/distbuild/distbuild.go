// Package distbuild builds a distribution of tt: a tt binary made of the tt
// core and the tt modules tt.manifest.toml lists.
//
// The build generates a main module in a work directory - a go.mod that
// requires the core and the modules and a main package that hands their
// constructors to core.Main - and compiles it with the go command, with the
// build tags and linker flags tt itself is built with. tt.manifest.lock
// pins what the binary was built from and tt.manifest.sum holds the go.sum
// of the build, so the same manifest builds the same binary again.
package distbuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build/backend"
	"github.com/tarantool/tt/v3/internal/buildflags"
)

const (
	// filePerm is the mode of the lock and the sum.
	filePerm = 0o644
	// binaryPerm is the mode of the binary.
	binaryPerm = 0o755
	// dirPerm is the mode of the directories the build creates.
	dirPerm = 0o755
	// smokeTimeout bounds the run of the built binary that checks it.
	smokeTimeout = time.Minute
)

var (
	// errStaleLock reports a lock that does not match the manifest.
	errStaleLock = errors.New("tt.manifest.lock is out of date")
	// errSumMismatch reports a build whose go.sum is not the locked one.
	errSumMismatch = errors.New("the Go modules of the build differ from tt.manifest.sum")
	// errLockedUpdate reports --locked and --update together.
	errLockedUpdate = errors.New("--locked and --update exclude each other")
	// errCoreLocked reports a core override in a build from the lock.
	errCoreLocked = errors.New("--core (or TT_DIST_CORE) excludes --locked: " +
		"a build on another core than the lock's is not reproducible")
	// errVendorAndOutput reports a bundle asked for with a binary or a work
	// directory of its own.
	errVendorAndOutput = errors.New("--vendor excludes --output and --work: " +
		"the bundle is the work directory and builds no binary")
)

// Options configure Run.
type Options struct {
	// Manifest is the path to tt.manifest.toml.
	Manifest string
	// Output is where the binary is written. Empty: the name of the
	// distribution in the manifest's directory.
	Output string
	// Locked builds strictly from the lock: the lock must match the
	// manifest, the versions are the locked ones, the go.sum of the build
	// must be tt.manifest.sum, and neither is written.
	Locked bool
	// Update resolves every version again, ignoring the lock.
	Update bool
	// WorkDir is where the main module is generated and kept. Empty: a
	// temporary directory, removed after the build.
	WorkDir string
	// Core overrides [core] of the manifest for this build: a local
	// checkout of the core - an existing directory, or a path that starts
	// with ./, ../ or / - or else a Go version query. A relative path is
	// taken from CoreDir. The core of such a build is not taken from the
	// lock, and the lock is not written: the build is a development one.
	// It excludes Locked.
	Core string
	// CoreDir is the directory a relative Core path is taken from.
	CoreDir string
	// Vendor makes a bundle in this directory instead of a binary: the main
	// module with every module it is built from vendored, and the build
	// parameters, which go build turns into the binary without a network,
	// a module cache or tt. It excludes Output and WorkDir.
	Vendor string
	// SSL is the TLS implementation, overriding [build].ssl: the value of
	// TT_CLI_BUILD_SSL. Empty: the manifest's.
	SSL string
	// Label follows the version of the distribution: the value of
	// VERSION_LABEL.
	Label string
	// GeneratedBy names the tt that writes the lock: "tt 3.1.0".
	GeneratedBy string
	// RunningCore is the version of the core of the tt that builds, which
	// a manifest without [core].tt and [core].path is built on. Empty:
	// unknown.
	RunningCore string
	// ShowOutput streams the output of the go command.
	ShowOutput bool
	// Environ is the environment of the go command. Nil: the process's.
	Environ []string

	// gocmd runs the go command; nil is the machine's.
	gocmd goCommand
}

// Result is what Run built.
type Result struct {
	// Output is the path to the binary, or to the bundle.
	Output string
	// Lock is what the binary was built from.
	Lock *manifest.DistributionLock
}

// Run builds the distribution opts.Manifest describes into opts.Output.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Locked && opts.Update {
		return nil, sdk.WithCode(sdk.ExitFailure, errLockedUpdate)
	}

	if opts.Vendor != "" && (opts.Output != "" || opts.WorkDir != "") {
		return nil, sdk.WithCode(sdk.ExitFailure, errVendorAndOutput)
	}

	if opts.Core != "" && opts.Locked {
		return nil, sdk.WithCode(sdk.ExitFailure, errCoreLocked)
	}

	if opts.gocmd == nil {
		opts.gocmd = execGo{showOutput: opts.ShowOutput}
	}

	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}

	dist, err := readManifest(opts.Manifest)
	if err != nil {
		return nil, err
	}

	if opts.Core != "" {
		dist.Core = overrideCore(dist.Core, opts.Core, opts.CoreDir)
	}

	dir := filepath.Dir(opts.Manifest)

	lock, sum, err := readLock(dir, dist, opts)
	if err != nil {
		return nil, err
	}

	ssl, err := sslOf(dist, opts)
	if err != nil {
		return nil, err
	}

	var (
		work    string
		cleanup = func() {}
	)

	if opts.Vendor != "" {
		work, err = prepareBundle(opts.Vendor)
	} else {
		work, cleanup, err = workDir(opts.WorkDir)
	}

	if err != nil {
		return nil, err
	}

	defer cleanup()

	b := builder{opts: opts, dist: dist, dir: dir, work: work, ssl: ssl, lock: lock, sum: sum}

	return b.build(ctx)
}

// builder is one build of a distribution.
type builder struct {
	opts Options
	dist *manifest.Distribution
	// dir is the manifest's directory.
	dir string
	// work is the directory of the main module.
	work string
	// ssl is the TLS implementation.
	ssl buildflags.SSL
	// lock is the lock the versions are taken from; nil re-resolves.
	lock *manifest.DistributionLock
	// sum is the locked go.sum, for a locked build.
	sum []byte
	// coreLdflags stamp the version of a local core into the binary.
	coreLdflags []string
}

// build resolves, generates, compiles and checks the distribution, then
// writes the binary or the bundle, the lock and the sum.
func (b builder) build(ctx context.Context) (*Result, error) {
	env := b.goEnv(b.ssl)
	res := resolver{
		gocmd: b.opts.gocmd, dir: b.dir, work: b.work, env: env,
		pinned: b.lock, unpinnedCore: b.opts.Core != "", runningCore: b.opts.RunningCore,
	}

	log.Infof("Resolving the core %s", b.dist.CoreModule())

	resolved, err := res.resolveCore(ctx, b.dist)
	if err != nil {
		return nil, err
	}

	logCore(resolved)

	// Go records no version for a core built from a directory, so the build
	// stamps the one git describe tells of it, as the magefile of tt does.
	if resolved.Dir != "" {
		b.coreLdflags = describe(ctx, resolved.Dir, "").ldflags()
	}

	modules, err := res.resolveModules(ctx, b.dist, resolved)
	if err != nil {
		return nil, err
	}

	logModules(modules)

	replaces, err := res.resolveReplaces(ctx, b.dist, resolved, modules)
	if err != nil {
		return nil, err
	}

	logReplaces(replaces)

	// A build on another core writes no lock, so it has nothing to hash.
	var hashes contentHashes

	if b.opts.Core == "" {
		hashes, err = b.hashLocal(resolved, modules, replaces)
		if err != nil {
			return nil, err
		}
	}

	if b.opts.Locked {
		err = b.checkContentHashes(resolved, modules, replaces, hashes)
		if err != nil {
			return nil, err
		}
	}

	generated, generatedModules, generatedReplaces := resolved, modules, replaces
	if b.opts.Vendor != "" {
		generated, generatedModules, generatedReplaces, err = b.localize(
			resolved, modules, replaces)
		if err != nil {
			return nil, err
		}
	}

	version := describe(ctx, b.dir, b.opts.Label)

	err = b.generate(generated, generatedModules, generatedReplaces, version)
	if err != nil {
		return nil, err
	}

	err = b.tidy(ctx, env)
	if err != nil {
		return nil, err
	}

	b.warnUnusedReplaces(ctx, env, replaces)

	var output string

	if b.opts.Vendor != "" {
		output, err = b.bundle(ctx, env, version)
	} else {
		output, err = b.binary(ctx, env)
	}

	if err != nil {
		return nil, err
	}

	lock := b.lockOf(resolved, modules, replaces, hashes)

	switch {
	case b.opts.Locked:
	case b.opts.Core != "":
		log.Infof("The core is overridden: %s is left as it is", manifest.DistributionLockFile)
	default:
		err = b.writeLock(lock)
		if err != nil {
			return nil, err
		}
	}

	log.Infof("Built %s", output)

	return &Result{Output: output, Lock: lock}, nil
}

// binary builds the binary, checks that it starts and installs it.
func (b builder) binary(ctx context.Context, env []string) (string, error) {
	log.Infof("Building %s", b.dist.Name())

	args, err := b.buildArgs(b.ssl, runtime.GOOS, b.dist.Name())
	if err != nil {
		return "", err
	}

	err = b.opts.gocmd.Run(ctx, b.work, env, args...)
	if err != nil {
		return "", err
	}

	built := filepath.Join(b.work, b.dist.Name())

	err = smoke(ctx, built, b.work, env)
	if err != nil {
		return "", err
	}

	output := b.output()

	err = install(built, output)
	if err != nil {
		return "", err
	}

	return output, nil
}

// generate writes the main module into the work directory.
func (b builder) generate(
	resolved core, modules []module, replaces []replacement, version versionInfo,
) error {
	gomod, err := generateGoMod(resolved, modules, replaces)
	if err != nil {
		return err
	}

	main, err := generateMain(mainSpec{
		CoreModule: resolved.Module,
		Modules:    modules,
		Options:    b.dist.Distribution,
		Version:    version,
	})
	if err != nil {
		return err
	}

	files := map[string][]byte{"go.mod": gomod, "main.go": main}
	if b.sum != nil {
		files["go.sum"] = b.sum
	} else {
		// A go.sum left in a kept work directory by an earlier build must
		// not leak into this one.
		files["go.sum"] = nil
	}

	for name, data := range files {
		err = os.WriteFile(filepath.Join(b.work, name), data, filePerm)
		if err != nil {
			return sdk.Errorf(sdk.ExitSystem, "writing %s: %w", name, err)
		}
	}

	return nil
}

// tidy tidies the main module and, for a locked build, checks its go.sum.
func (b builder) tidy(ctx context.Context, env []string) error {
	err := b.opts.gocmd.Run(ctx, b.work, env, "mod", "tidy")
	if err != nil {
		return err
	}

	if b.opts.Locked {
		return b.checkSum()
	}

	return nil
}

// warnUnusedReplaces warns of an entry of [replace] whose module the tidied
// go.mod does not require: no package of the build comes from it, so the
// replacement is not used, and a mistyped key is the likely reason. The
// check is advisory and never fails the build.
func (b builder) warnUnusedReplaces(ctx context.Context, env []string, replaces []replacement) {
	if len(replaces) == 0 {
		return
	}

	gomod, err := readGoMod(ctx, b.opts.gocmd, b.work, env, filepath.Join(b.work, "go.mod"))
	if err != nil {
		log.Debugf("checking the replacements: %s", err)

		return
	}

	for _, rep := range replaces {
		used := slices.ContainsFunc(gomod.Require, func(req goModRequire) bool {
			return req.Path == rep.Module && (rep.Version == "" || req.Version == rep.Version)
		})
		if !used {
			log.Warnf("replace %q: the build does not use %s, so the replacement "+
				"has no effect", rep.Key, rep.Module)
		}
	}
}

// buildArgs are the arguments of go build that builds output for goos with
// ssl.
func (b builder) buildArgs(ssl buildflags.SSL, goos, output string) ([]string, error) {
	tags, ldflags, err := b.flags(ssl, goos)
	if err != nil {
		return nil, err
	}

	return []string{
		"build", "-trimpath", "-tags", tags, "-ldflags", ldflags, "-o", output, ".",
	}, nil
}

// flags returns the -tags and the -ldflags of a build for goos with ssl: the
// version of a local core and the canonical ones of the TLS implementation,
// the manifest's after them.
func (b builder) flags(ssl buildflags.SSL, goos string) (string, string, error) {
	tags := slices.Concat(ssl.Tags(), b.dist.Build.Tags)
	ldflags := slices.Concat([]string{"-s", "-w"}, b.coreLdflags, ssl.Ldflags(goos),
		b.dist.Build.Ldflags)

	quoted, err := backend.QuoteFlags(ldflags)
	if err != nil {
		return "", "", sdk.WithCode(sdk.ExitFailure, fmt.Errorf("build.ldflags: %w", err))
	}

	return strings.Join(tags, ","), quoted, nil
}

// cgo reports whether a build with ssl uses cgo: as the TLS implementation
// needs, unless the manifest says.
func (b builder) cgo(ssl buildflags.SSL) bool {
	if b.dist.Build.Cgo != nil {
		return *b.dist.Build.Cgo
	}

	return ssl.Cgo()
}

// goEnv is the environment of the go command: the process's, without a
// workspace, resolving modules as the generated go.mod says, with cgo as
// ssl and the manifest ask for.
func (b builder) goEnv(ssl buildflags.SSL) []string {
	cgoEnabled := "CGO_ENABLED=0"
	if b.cgo(ssl) {
		cgoEnabled = "CGO_ENABLED=1"
	}

	return append(slices.Clone(b.opts.Environ), "GOWORK=off", "GOFLAGS=-mod=mod", cgoEnabled)
}

// checkSum compares the go.sum of the build with the locked one.
func (b builder) checkSum() error {
	got, err := os.ReadFile(filepath.Join(b.work, "go.sum"))
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "reading go.sum: %w", err)
	}

	if !bytes.Equal(got, b.sum) {
		return sdk.WithCode(sdk.ExitFailure, fmt.Errorf("%w: rebuild without --locked "+
			"to update it", errSumMismatch))
	}

	return nil
}

// lockOf describes the build as a lock: what it was built from, not how it
// was compiled - the TLS implementation, the tags and the toolchain are the
// choice of each build of the same lock.
func (b builder) lockOf(
	resolved core, modules []module, replaces []replacement, hashes contentHashes,
) *manifest.DistributionLock {
	lock := &manifest.DistributionLock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: b.dist.ManifestVersion,
		GeneratedBy:     b.opts.GeneratedBy,
		ManifestHash:    b.dist.Hash(),
		Core: manifest.LockedCore{
			Module:      resolved.Module,
			Version:     resolved.Version,
			Revision:    resolved.Revision,
			Path:        resolved.Path,
			ContentHash: hashes.core,
			Repository:  nil,
		},
		Modules: nil,
		Replace: nil,
	}

	for _, repo := range resolved.Repository {
		if repo.Version == "" {
			continue
		}

		if lock.Core.Repository == nil {
			lock.Core.Repository = map[string]string{}
		}

		lock.Core.Repository[repo.Module] = repo.Version
	}

	for _, mod := range modules {
		if lock.Modules == nil {
			lock.Modules = map[string]manifest.LockedModule{}
		}

		lock.Modules[mod.Name] = manifest.LockedModule{
			Module: mod.Module, Version: mod.Version, Path: mod.Path,
			ContentHash: hashes.modules[mod.Name],
		}
	}

	for _, rep := range replaces {
		if lock.Replace == nil {
			lock.Replace = map[string]manifest.LockedReplace{}
		}

		lock.Replace[rep.Key] = manifest.LockedReplace{
			Module: rep.Target, Version: rep.TargetVersion, Path: rep.Path,
			ContentHash: hashes.replaces[rep.Key],
		}
	}

	return lock
}

// writeLock writes the lock and the go.sum of the build next to the
// manifest, unless the ones there pin the same: a build by another tt would
// change no more than generated_by, which is no reason to touch them.
func (b builder) writeLock(lock *manifest.DistributionLock) error {
	data, err := lock.Marshal()
	if err != nil {
		return err
	}

	sum, err := os.ReadFile(filepath.Join(b.work, "go.sum"))
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "reading go.sum: %w", err)
	}

	if b.lockWritten(lock, sum) {
		return nil
	}

	err = writeFileAtomic(filepath.Join(b.dir, manifest.DistributionLockFile), data)
	if err != nil {
		return err
	}

	return writeFileAtomic(filepath.Join(b.dir, manifest.DistributionSumFile), sum)
}

// lockWritten reports whether the lock and the sum next to the manifest are
// lock and sum already, generated_by aside.
func (b builder) lockWritten(lock *manifest.DistributionLock, sum []byte) bool {
	data, err := os.ReadFile(filepath.Join(b.dir, manifest.DistributionLockFile))
	if err != nil {
		return false
	}

	written, err := os.ReadFile(filepath.Join(b.dir, manifest.DistributionSumFile))
	if err != nil || !bytes.Equal(written, sum) {
		return false
	}

	existing, err := manifest.ParseDistributionLock(data)
	if err != nil {
		return false
	}

	probe := *lock

	probe.GeneratedBy = existing.GeneratedBy

	// The bytes are compared, not the parsed locks, so that a lock with
	// fields this tt no longer writes is written anew.
	marshalled, err := probe.Marshal()

	return err == nil && bytes.Equal(marshalled, data)
}

// output is where the binary goes.
func (b builder) output() string {
	if b.opts.Output != "" {
		return b.opts.Output
	}

	return filepath.Join(b.dir, b.dist.Name())
}

// overrideCore returns base with the core value names: a local checkout
// when value is an existing directory or starts with ./, ../ or /, taken
// from dir when relative, and a Go version query otherwise. The module
// stays the manifest's.
func overrideCore(base manifest.DistributionCore, value, dir string) manifest.DistributionCore {
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}

	info, err := os.Stat(path)
	isDir := err == nil && info.IsDir()

	if isDir || value == "." || value == ".." || strings.HasPrefix(value, "./") ||
		strings.HasPrefix(value, "../") || filepath.IsAbs(value) {
		return manifest.DistributionCore{Tt: "", Path: path, Module: base.Module}
	}

	return manifest.DistributionCore{Tt: value, Path: "", Module: base.Module}
}

// readManifest reads and checks tt.manifest.toml.
func readManifest(path string) (*manifest.Distribution, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, sdk.Errorf(sdk.ExitFailure, "%s not found: a distribution of tt is "+
				"described by %s", path, manifest.DistributionFile)
		}

		return nil, sdk.Errorf(sdk.ExitSystem, "reading %s: %w", path, err)
	}

	dist, warnings, err := manifest.ParseDistribution(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	for _, warning := range warnings {
		log.Warnf("%s: %s", path, warning)
	}

	err = dist.Validate()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return dist, nil
}

// readLock reads the lock and the sum in dir, and returns the lock the
// build takes its versions from: the lock, when it matches the manifest and
// opts do not ask for an update; nil otherwise. A locked build requires both
// and returns the sum it must reproduce.
func readLock(
	dir string, dist *manifest.Distribution, opts Options,
) (*manifest.DistributionLock, []byte, error) {
	if opts.Update {
		return nil, nil, nil
	}

	data, err := os.ReadFile(filepath.Join(dir, manifest.DistributionLockFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !opts.Locked {
			return nil, nil, nil
		}

		return nil, nil, lockError(err)
	}

	lock, err := manifest.ParseDistributionLock(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", manifest.DistributionLockFile, err)
	}

	if lock.ManifestHash != dist.Hash() {
		if opts.Locked {
			return nil, nil, sdk.WithCode(sdk.ExitFailure, fmt.Errorf("%w: "+
				"%s changed since; rebuild without --locked to update it",
				errStaleLock, manifest.DistributionFile))
		}

		return nil, nil, nil
	}

	if !opts.Locked {
		return lock, nil, nil
	}

	sum, err := os.ReadFile(filepath.Join(dir, manifest.DistributionSumFile))
	if err != nil {
		return nil, nil, lockError(err)
	}

	return lock, sum, nil
}

// lockError describes a lock or sum that cannot be read.
func lockError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return sdk.Errorf(sdk.ExitFailure, "--locked: %w: build without --locked first", err)
	}

	return sdk.Errorf(sdk.ExitSystem, "%w", err)
}

// sslOf returns the TLS implementation of the build: opts.SSL, the value of
// TT_CLI_BUILD_SSL, when set, and the manifest's otherwise.
func sslOf(dist *manifest.Distribution, opts Options) (buildflags.SSL, error) {
	name := dist.Build.SSL
	if opts.SSL != "" {
		name = opts.SSL
	}

	ssl, err := buildflags.Parse(name)
	if err != nil {
		return "", sdk.WithCode(sdk.ExitFailure, err)
	}

	return ssl, nil
}

// workDir returns the directory of the main module and the function that
// cleans it up: dir, kept, or a temporary directory, removed.
func workDir(dir string) (string, func(), error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", nil, sdk.Errorf(sdk.ExitFailure, "--work %s: %w", dir, err)
		}

		err = os.MkdirAll(abs, dirPerm)
		if err != nil {
			return "", nil, sdk.Errorf(sdk.ExitSystem, "--work %s: %w", dir, err)
		}

		return abs, func() {}, nil
	}

	tmp, err := os.MkdirTemp("", "tt-distribution-")
	if err != nil {
		return "", nil, sdk.Errorf(sdk.ExitSystem, "creating a work directory: %w", err)
	}

	return tmp, func() { _ = os.RemoveAll(tmp) }, nil
}

// smoke runs the built binary's version command: a module whose commands
// cannot be mounted fails it, and should fail the build rather than its
// first user.
func smoke(ctx context.Context, binary, dir string, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "version")

	cmd.Dir = dir
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "the built binary does not start: %s version: %w\n%s",
			binary, err, bytes.TrimSpace(out))
	}

	log.Debugf("%s", bytes.TrimSpace(out))

	return nil
}

// install moves the binary from the work directory to output.
func install(built, output string) error {
	err := os.MkdirAll(filepath.Dir(output), dirPerm)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "creating the directory of %s: %w", output, err)
	}

	err = os.Rename(built, output)
	if err == nil {
		return nil
	}

	// Another file system: copy.
	src, err := os.Open(built)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "installing %s: %w", output, err)
	}

	defer func() { _ = src.Close() }()

	tmp := output + ".tmp"

	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, binaryPerm)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "installing %s: %w", output, err)
	}

	_, err = io.Copy(dst, src)

	err = errors.Join(err, dst.Close())
	if err == nil {
		err = os.Rename(tmp, output)
	}

	if err != nil {
		_ = os.Remove(tmp)

		return sdk.Errorf(sdk.ExitSystem, "installing %s: %w", output, err)
	}

	return nil
}

// writeFileAtomic writes data to path through a temporary file renamed over
// it, so a reader sees the old file or the new one.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "writing %s: %w", path, err)
	}

	_, writeErr := tmp.Write(data)

	err = errors.Join(writeErr, tmp.Chmod(filePerm), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return sdk.Errorf(sdk.ExitSystem, "writing %s: %w", path, err)
	}

	return nil
}

// logCore tells what core the distribution is built on.
func logCore(resolved core) {
	if resolved.Dir != "" {
		log.Infof("  core %s (path %s)", resolved.Module, resolved.Path)

		return
	}

	log.Infof("  core %s@%s", resolved.Module, resolved.Version)
}

// logReplaces tells what Go modules the build replaces.
func logReplaces(replaces []replacement) {
	for _, rep := range replaces {
		if rep.Dir != "" {
			log.Infof("  replace %s (path %s)", rep.Key, rep.Path)
		} else {
			log.Infof("  replace %s %s@%s", rep.Key, rep.Target, rep.TargetVersion)
		}
	}
}

// logModules tells what tt modules the distribution has.
func logModules(modules []module) {
	for _, mod := range modules {
		switch {
		case mod.Dir != "":
			log.Infof("  %s %s (path %s)", mod.Name, mod.Module, mod.Path)
		case mod.Version != "":
			log.Infof("  %s %s@%s", mod.Name, mod.Module, mod.Version)
		default:
			log.Infof("  %s %s (with the core)", mod.Name, mod.Module)
		}
	}
}
