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

var (
	errReplyForDescriptionIsMandatoryForModule = errors.New(
		"reply for --description is mandatory for module",
	)
	errReplyForVersionIsMandatoryForModule = errors.New(
		"reply for --version is mandatory for module",
	)
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

// fillManifest update Manifest required fields, by calls external module `main`
// with both `description` and `version` flags and parse reply. The
// executable is read through open before it is called.
func fillManifest(manifest Manifest, open Opener) (Manifest, error) {
	err := check(manifest.Main, open)
	if err != nil {
		return manifest, err
	}

	out, err := exec.CommandContext(
		context.Background(), manifest.Main, "--description", "--version").Output()
	if err != nil {
		return manifest, fmt.Errorf("failed to get module info: %w", err)
	}

	var info struct {
		Version string `yaml:"version"`
		Help    string `yaml:"help"`
	}

	err = yaml.Unmarshal(out, &info)
	if err != nil {
		return manifest, fmt.Errorf("can't parse module info: %w", err)
	}

	if info.Version == "" {
		return manifest, errReplyForVersionIsMandatoryForModule
	}

	help := firstLine(info.Help)
	if help == "" {
		return manifest, errReplyForDescriptionIsMandatoryForModule
	}

	manifest.Version = info.Version
	manifest.Help = help

	return manifest, nil
}
