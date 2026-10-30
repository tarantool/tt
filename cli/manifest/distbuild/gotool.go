package distbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/util"
)

// goTool is the program the build runs.
const goTool = "go"

// errNoGo reports that the go command is not found.
var errNoGo = errors.New("go is not found in PATH: building tt needs the Go toolchain")

// networkFailures are the phrases the go command prints when it cannot
// reach a module source: such a failure is the system's, not the manifest's.
var networkFailures = []string{
	"dial tcp", "i/o timeout", "connection refused", "connection reset",
	"no such host", "TLS handshake timeout", "network is unreachable",
	"Temporary failure in name resolution",
}

// goCommand runs the go command.
type goCommand interface {
	// Output runs go with args in dir and returns its standard output. A
	// failure carries what go printed to its standard error.
	Output(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)
	// Run runs go with args in dir, showing its output when the build is
	// verbose and when it fails.
	Run(ctx context.Context, dir string, env []string, args ...string) error
}

// execGo is the go command of the machine.
type execGo struct {
	// showOutput streams the output of Run.
	showOutput bool
}

// Output implements goCommand.
func (execGo) Output(
	ctx context.Context, dir string, env []string, args ...string,
) ([]byte, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, goTool, args...)

	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, goError(args, err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// Run implements goCommand.
func (g execGo) Run(ctx context.Context, dir string, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, goTool, args...)

	cmd.Env = env

	err := util.RunCommand(cmd, dir, g.showOutput)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return sdk.WithCode(sdk.ExitSystem, errNoGo)
		}

		return sdk.Errorf(sdk.ExitSystem, "go %s: %w", strings.Join(args, " "), err)
	}

	return nil
}

// goError describes a failed go command: a missing go and a network failure
// are the system's (sdk.ExitSystem), anything else - a module or a version
// that does not exist - is the manifest's.
func goError(args []string, err error, stderr string) error {
	if errors.Is(err, exec.ErrNotFound) {
		return sdk.WithCode(sdk.ExitSystem, errNoGo)
	}

	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}

	code := sdk.ExitFailure

	for _, phrase := range networkFailures {
		if strings.Contains(msg, phrase) {
			code = sdk.ExitSystem

			break
		}
	}

	return sdk.Errorf(code, "go %s: %s", strings.Join(args, " "), msg)
}

// moduleInfo is what go list -m -json reports about a module version.
type moduleInfo struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	GoMod   string        `json:"GoMod"`
	Origin  *moduleOrigin `json:"Origin"`
}

// moduleOrigin is where the go command found a module version.
type moduleOrigin struct {
	// Hash is the commit.
	Hash string `json:"Hash"`
}

// listModule resolves the version query of module to an exact version.
func listModule(
	ctx context.Context, gocmd goCommand, dir string, env []string, module, query string,
) (moduleInfo, error) {
	out, err := gocmd.Output(ctx, dir, env, "list", "-m", "-json", module+"@"+query)
	if err != nil {
		return moduleInfo{}, err
	}

	var info moduleInfo

	err = json.Unmarshal(out, &info)
	if err != nil {
		return moduleInfo{}, fmt.Errorf("go list -m -json %s@%s: %w", module, query, err)
	}

	if info.Version == "" {
		return moduleInfo{}, sdk.Errorf(sdk.ExitFailure, "%s@%s: go reported no version",
			module, query)
	}

	return info, nil
}

// goModFile is what go mod edit -json reports about a go.mod file.
type goModFile struct {
	Module  goModVersion   `json:"Module"`
	Go      string         `json:"Go"`
	Replace []goModReplace `json:"Replace"`
}

// goModVersion is a module path with an optional version, or a directory.
type goModVersion struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

// goModReplace is a replace directive.
type goModReplace struct {
	Old goModVersion `json:"Old"`
	New goModVersion `json:"New"`
}

// readGoMod reads the go.mod file at path.
func readGoMod(
	ctx context.Context, gocmd goCommand, dir string, env []string, path string,
) (goModFile, error) {
	out, err := gocmd.Output(ctx, dir, env, "mod", "edit", "-json", path)
	if err != nil {
		return goModFile{}, err
	}

	var file goModFile

	err = json.Unmarshal(out, &file)
	if err != nil {
		return goModFile{}, fmt.Errorf("go mod edit -json %s: %w", path, err)
	}

	return file, nil
}
