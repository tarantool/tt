package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// cargoTool is the program the cargo backend runs.
const cargoTool = "cargo"

// cargoArches maps a GOARCH to the architecture of a Rust target triple.
var cargoArches = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

// cargoBackend builds a Rust binary with cargo build and copies it into
// TT_OUTPUT_DIR under its bin name.
//
// Where cargo puts the binary depends on the target, the profile and the
// target directory the project or the environment configures, so the path is
// taken from what cargo reports with --message-format, never guessed.
type cargoBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// Run builds the binary of build in cwd as
// cargo build --profile <profile> --bin <bin> and copies it to
// $TT_OUTPUT_DIR/<bin>.
func (c cargoBackend) Run(ctx context.Context, build manifest.Build, cwd string, env Env) error {
	err := requireAbsPaths(cwd, env.OutputDir)
	if err != nil {
		return err
	}

	err = Preflight(build, env)
	if err != nil {
		return err
	}

	bin := build.Executables(env.Component)[0]

	target, err := cargoTarget(build, env)
	if err != nil {
		return err
	}

	stdout, err := runCapture(ctx, cwd, env.environ(), c.showOutput, cargoTool,
		cargoArgs(build, bin, target)...)
	if err != nil {
		if build.StaticValue() {
			return fmt.Errorf("%w; a static build is made for the target %s, "+
				"which rustup target add %s installs", err, target, target)
		}

		return err
	}

	executable, err := cargoExecutable(stdout, bin)
	if err != nil {
		return err
	}

	err = copyFile(executable, filepath.Join(env.OutputDir, bin))
	if err != nil {
		return fmt.Errorf("copy the binary %q: %w", executable, err)
	}

	return nil
}

// cargoArgs builds the cargo build argv. Diagnostics are rendered by cargo
// to stderr as usual, and only the JSON messages go to stdout.
func cargoArgs(build manifest.Build, bin, target string) []string {
	profile := build.Profile
	if profile == "" {
		profile = manifest.DefaultCargoProfile
	}

	args := []string{
		"build", "--profile", profile, "--bin", bin,
		"--message-format=json-render-diagnostics",
	}

	if build.Package != "" {
		args = append(args, "--package", build.Package)
	}

	if len(build.Features) > 0 {
		args = append(args, "--features", strings.Join(build.Features, ","))
	}

	if target != "" {
		args = append(args, "--target", target)
	}

	return args
}

// cargoTarget picks the --target of a build for the build platform, or none
// when cargo's own host target does.
//
// A static linux program is built for <arch>-unknown-linux-musl, which links
// the C library in: the gnu targets link glibc dynamically. A build for the
// host takes no --target otherwise, so that the project's own configuration
// of its host builds applies; a build for another platform names the gnu or
// apple target of that platform.
func cargoTarget(build manifest.Build, env Env) (string, error) {
	goos, goarch := env.platformOS(), env.platformArch()

	arch, ok := cargoArches[goarch]
	if !ok {
		return "", fmt.Errorf("%w %s-%s", errUnsupportedTarget, goos, goarch)
	}

	host := goos == runtime.GOOS && goarch == runtime.GOARCH

	switch goos {
	case "linux":
		if build.StaticValue() {
			return arch + "-unknown-linux-musl", nil
		}

		if host {
			return "", nil
		}

		return arch + "-unknown-linux-gnu", nil
	case darwinOS:
		if host {
			return "", nil
		}

		return arch + "-apple-darwin", nil
	default:
		return "", fmt.Errorf("%w %s-%s", errUnsupportedTarget, goos, goarch)
	}
}

// cargoMessage is the part of a cargo JSON message the backend reads: a
// compiler-artifact message names the target it built and, for a binary, the
// executable it produced.
type cargoMessage struct {
	Reason string `json:"reason"`
	Target struct {
		Kind []string `json:"kind"`
		Name string   `json:"name"`
	} `json:"target"`
	Executable string `json:"executable"`
}

// cargoExecutable finds the executable of the binary bin among the JSON
// messages cargo printed. A line that is not a JSON message - a build script
// may print to stdout - is skipped.
func cargoExecutable(stdout []byte, bin string) (string, error) {
	executable := ""

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(nil, len(stdout)+1)

	for scanner.Scan() {
		var msg cargoMessage

		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}

		if msg.Reason == "compiler-artifact" && msg.Executable != "" &&
			msg.Target.Name == bin && slices.Contains(msg.Target.Kind, "bin") {
			executable = msg.Executable
		}
	}

	err := scanner.Err()
	if err != nil {
		return "", fmt.Errorf("reading the cargo messages: %w", err)
	}

	if executable == "" {
		return "", fmt.Errorf("%w for the binary %q", errNoArtifact, bin)
	}

	return executable, nil
}

// runCapture runs a subprocess like runEnviron, but returns its stdout: the
// cargo backend reads its JSON messages from there. The rest follows
// util.RunCommand: stderr is streamed with showOutput, and otherwise kept and
// shown only when the command fails, with a spinner while it runs.
func runCapture(
	ctx context.Context, cwd string, environ []string,
	showOutput bool, name string, args ...string,
) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	cmd.Env = environ
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	stopSpinner := func() {}

	if showOutput {
		cmd.Stderr = os.Stderr
	} else {
		stopSpinner = log.Spinner("running " + strings.Join(cmd.Args, " "))
	}

	err := cmd.Run()

	stopSpinner()

	if err != nil {
		_, _ = os.Stderr.Write(stderr.Bytes())

		return nil, fmt.Errorf("failed to run \n%s\n\n%w", cmd.String(), err)
	}

	return stdout.Bytes(), nil
}
