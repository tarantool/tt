package extmod

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/exitcode"
)

// signaledExitCode is tt's exit code for an external module killed by a
// signal, which has no exit status of its own.
const signaledExitCode = 255

// check reads the file at path through open to its end, so that the file
// fails whichever way the integrity checks refuse it: when it is opened or
// while it is read.
func check(path string, open Opener) error {
	file, err := open(path)
	if err != nil {
		return fmt.Errorf("integrity check failed for %q: %w", path, err)
	}

	_, err = io.Copy(io.Discard, file)

	err = errors.Join(err, file.Close())
	if err != nil {
		return fmt.Errorf("integrity check failed for %q: %w", path, err)
	}

	return nil
}

// runModule runs the executable main with args, attached to streams, once
// it has been read through open. A refused executable is not run.
func runModule(main string, args []string, open Opener, streams output.Streams) error {
	err := check(main, open)
	if err != nil {
		return err
	}

	return runExec(main, args, streams)
}

// runExec runs command with the supplied arguments, attached to streams.
//
// A command that exits with a non-zero status yields a silent error carrying
// that status as its exit code: the module has spoken for itself on its own
// streams, and tt only passes the status on. A command killed by a signal
// exits signaledExitCode. A command that cannot be started at all is an
// ordinary error.
func runExec(command string, args []string, streams output.Streams) error {
	cmd := exec.CommandContext(context.Background(), command, args...)

	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err
	cmd.Stdin = streams.In

	err := cmd.Run()
	if err == nil {
		return nil
	}

	var exitError *exec.ExitError

	if !errors.As(err, &exitError) {
		return fmt.Errorf("failed to exec external module: %w", err)
	}

	code := exitError.ExitCode()
	if code < 0 {
		code = signaledExitCode
	}

	return sdk.WithCode(code, exitcode.Silent(
		fmt.Errorf("external module %q: %w", command, err)))
}

// moduleHelp calls the external module main, once it has been read through
// open, with the --help flag and returns its output.
func moduleHelp(main string, open Opener) (string, error) {
	err := check(main, open)
	if err != nil {
		return "", err
	}

	out, err := exec.CommandContext(context.Background(), main, "--help").Output()
	if err != nil {
		return "", fmt.Errorf("%s --help: %w", main, err)
	}

	return string(out), nil
}

// fillManifest fills the version and the help of manifest from the reply of
// its executable to --description --version: the one cacheDir keeps for the
// executable as it is now, or else the one it gives when it is called, read
// through open first, which cacheDir then keeps. An executable that fails,
// or cannot be run, is an error, and is asked again next time.
func fillManifest(manifest Manifest, open Opener, cacheDir string) (Manifest, error) {
	key, keyed := replyKey(manifest.Main)
	if keyed {
		cached, found := loadReply(cacheDir, key)
		if found {
			manifest.Version, manifest.Help = cached.Version, cached.Help

			return manifest, nil
		}
	}

	err := check(manifest.Main, open)
	if err != nil {
		return manifest, err
	}

	out, err := exec.CommandContext(
		context.Background(), manifest.Main, "--description", "--version").Output()
	if err != nil {
		// What the module said on stderr is why it failed, as far as it
		// knows.
		if exitError, ok := errors.AsType[*exec.ExitError](err); ok {
			if said := firstLine(string(exitError.Stderr)); said != "" {
				return manifest, fmt.Errorf("failed to get module info: %w: %s", err, said)
			}
		}

		return manifest, fmt.Errorf("failed to get module info: %w", err)
	}

	manifest.Version, manifest.Help = parseReply(out)

	if keyed {
		key.Version, key.Help = manifest.Version, manifest.Help
		storeReply(cacheDir, key)
	}

	return manifest, nil
}

// parseReply returns the version and the one-line help a module's reply to
// --description --version gives. A YAML mapping with a version or a help
// gives them; any other reply is a description in plain text, its first
// line that is not empty, and the version is unknown.
func parseReply(out []byte) (string, string) {
	var info struct {
		Version string `yaml:"version"`
		Help    string `yaml:"help"`
	}

	err := yaml.Unmarshal(out, &info)
	if err == nil && (info.Version != "" || firstLine(info.Help) != "") {
		return info.Version, firstLine(info.Help)
	}

	return "", firstLine(string(out))
}
