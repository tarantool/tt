package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/scope"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/runtimepin"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/mirror"
)

// downloadBinariesHelp is what the help of tt package download says about the
// programs it mirrors.
const downloadBinariesHelp = " With no arguments, the tarantool, the tt and, with " +
	"[platform].tcm, the TCM the lock pins (tt package resolve pins them) are downloaded " +
	"for this machine too, into DIR/" + mirror.DirName + ", indexed with their sha256 in " +
	mirror.IndexFile + ": the installation of exactly that version in the project, " +
	"repacked, or else the archive the source tt binary install takes it from has; a " +
	"tarantool with its headers and the EmmyLua stubs it ships. tt binary install " +
	"--registry DIR installs them with no network. A program the lock pins no version " +
	"of fails the download, after the rest is downloaded. With --" + noBinariesFlag +
	" no programs are downloaded and the lock need not pin them: the mirror holds the " +
	"rocks and what the builds download."

// noBinariesFlag is the flag of tt package download that mirrors no programs.
const noBinariesFlag = "no-binaries"

// noBinariesUsage is the help of --no-binaries.
const noBinariesUsage = "do not download the tarantool, tt and TCM the lock pins into DIR/" +
	mirror.DirName

// errRuntimeNotPinned reports a program of [platform] the lock pins no
// version of, which tt package download cannot mirror.
var errRuntimeNotPinned = errors.New("the lock pins no version")

// errStalePin reports a pin of the lock [platform] no longer allows.
var errStalePin = errors.New("the pin does not satisfy [platform]")

// downloadBinariesInto puts the programs the lock of the project in
// projectDir pins into the mirror dir for this machine (mirror.Add), and
// prints the archives. Every program is tried; the error names each one that
// failed.
func downloadBinariesInto(ctx context.Context, projectDir, dir string) error {
	man, lock, err := readManifestAndLock(projectDir)
	if err != nil {
		return err
	}

	root := scope.ProjectRoot(projectDir)

	var (
		failed []error
		added  int
	)

	for _, component := range runtimepin.Components() {
		if !runtimepin.Wanted(man.Platform, component) {
			continue
		}

		result, err := addBinary(ctx, dir, root, man.Platform, lock, component)
		if err != nil {
			failed = append(failed, err)

			continue
		}

		for _, warning := range result.Warnings {
			log.Warn(warning)
		}

		added++

		_, _ = fmt.Fprintln(os.Stdout, result.Path)
	}

	if added > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "indexed %d program(s) in %s\n", added,
			filepath.Join(dir, mirror.DirName))
	}

	return errors.Join(failed...)
}

// addBinary puts the version of the component the lock pins into the mirror
// dir: the installation of it in the project scope rooted at root, else the
// archive the source of the product has.
func addBinary(ctx context.Context, dir, root string, platform manifest.Platform,
	lock *manifest.Lock, component runtimepin.Component,
) (mirror.Result, error) {
	program := runtimepin.Program(platform, component)
	constraint := runtimepin.Constraint(platform, component)
	pin := runtimepin.Get(lock, component)

	switch {
	case pin == "":
		return mirror.Result{}, fmt.Errorf("%w of %s: run tt package resolve to pin the %s "+
			"that satisfies %q of [platform].%s, or download with --%s, which skips the "+
			"programs", errRuntimeNotPinned, program, program, constraint.Version, component,
			noBinariesFlag)
	case !runtimepin.Satisfies(pin, constraint):
		return mirror.Result{}, fmt.Errorf("%w: %s pins %s %s, which does not satisfy %q of "+
			"[platform].%s: run tt package resolve, or download with --%s, which skips the "+
			"programs", errStalePin, lockFileName, program, pin, constraint.Version, component,
			noBinariesFlag)
	}

	installation := binary.InstallationPath(root, program, pin)
	if !binary.IsRegularFile(filepath.Join(installation, program.Exec())) {
		installation = ""
	}

	var source bundle.Source

	found, err := binary.BundleSource(program)
	if err == nil {
		source = found
	}

	result, err := mirror.Add(ctx, dir, mirror.Request{
		Program:      program,
		Version:      pin,
		Installation: installation,
		Source:       source,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
	})
	if err != nil {
		return mirror.Result{}, fmt.Errorf("downloading %s %s, which %s pins: %w",
			program, pin, lockFileName, err)
	}

	return result, nil
}

// readManifestAndLock reads the manifest and the lock of the project in
// projectDir.
func readManifestAndLock(projectDir string) (*manifest.Manifest, *manifest.Lock, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, manifestFileName))
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", manifestFileName, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", manifestFileName, err)
	}

	data, err = os.ReadFile(filepath.Join(projectDir, lockFileName))
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", lockFileName, err)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", lockFileName, err)
	}

	return man, lock, nil
}
