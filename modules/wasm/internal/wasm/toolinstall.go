package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/fetch"
)

// toolPerm is the mode of a program tt installs.
const toolPerm os.FileMode = 0o755

var (
	// errNoAsset reports a tool with no build for the host.
	errNoAsset = errors.New("no build for this host")
	// errNoMember reports an archive that does not hold the program.
	errNoMember = errors.New("the archive does not hold the program")
	// errToolVersion reports an installed tool that reports another version.
	errToolVersion = errors.New("the installed tool reports another version")
)

// ToolchainOptions configure EnsureToolchain.
type ToolchainOptions struct {
	// Root is the .tarantool of the project.
	Root string
	// Language is the language whose toolchain is ensured.
	Language string
	// Fetcher downloads the tools tt installs.
	Fetcher Fetcher
	// TarawasmEnv is the environment of tarawasm, where pip installs; empty
	// when the project has no tarawasm.
	TarawasmEnv string
	// Output receives the output of pip.
	Output io.Writer
}

// ToolchainResult is what EnsureToolchain found and installed.
type ToolchainResult struct {
	// Statuses are the tools of the toolchain, as checked.
	Statuses []ToolStatus
	// Unlinked are the programs tt installed without linking them into the
	// project's bin, by tool: tarawasm is told of them in each component.
	Unlinked map[string]string
}

// EnsureToolchain installs the tools of the language that tt installs into
// the project, and checks every tool of the language: a tool tt installed
// where it installed it, a tool the user installs as a build finds it, in the
// project's bin directory first and then on PATH. A tool the user has to
// install, or to upgrade, is an error wrapping ErrToolchain; nothing is
// installed into the system.
func EnsureToolchain(ctx context.Context, opts ToolchainOptions) (*ToolchainResult, error) {
	tools, err := Toolchain(opts.Language)
	if err != nil {
		return nil, err
	}

	result := &ToolchainResult{Statuses: nil, Unlinked: map[string]string{}}
	installed := map[string]string{}

	for _, tool := range tools {
		if tool.Release == nil {
			continue
		}

		program, err := installTool(ctx, tool, opts)
		if err != nil {
			return nil, fmt.Errorf("installing %s %s: %w", tool.Name, tool.Release.Version, err)
		}

		installed[tool.Name] = program

		if tool.Release.Unlinked {
			result.Unlinked[tool.Name] = program
		}
	}

	path := filepath.Join(opts.Root, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")

	result.Statuses, err = checkTools(ctx, tools, path, installed, opts.Language)

	return result, err
}

// checkToolchain checks every tool in the directories of path; a tool that
// does not fit is an error wrapping ErrToolchain, which says what to install.
func checkToolchain(
	ctx context.Context, tools []Tool, path, language string,
) ([]ToolStatus, error) {
	return checkTools(ctx, tools, path, nil, language)
}

// checkTools is checkToolchain with the programs tt installed, by tool,
// checked where they are rather than looked for.
func checkTools(
	ctx context.Context, tools []Tool, path string, installed map[string]string,
	language string,
) ([]ToolStatus, error) {
	statuses := make([]ToolStatus, 0, len(tools))
	missing := []string{}

	for _, tool := range tools {
		var status ToolStatus

		if program, ok := installed[tool.Name]; ok {
			status = checkProgram(ctx, tool, program)
		} else {
			status = CheckTool(ctx, tool, path)
		}

		statuses = append(statuses, status)

		if !status.OK {
			missing = append(missing, fmt.Sprintf("%s %s (%s): %s",
				tool.Name, tool.Requirement(), status.Problem(), tool.Hint))
		}
	}

	if len(missing) > 0 {
		return statuses, fmt.Errorf("%w for %s; install, tt does not:\n  %s", ErrToolchain,
			language, strings.Join(missing, "\n  "))
	}

	return statuses, nil
}

// FormatToolchain renders the statuses of a toolchain one tool a line.
func FormatToolchain(language string, statuses []ToolStatus) string {
	var out strings.Builder

	fmt.Fprintf(&out, "%s toolchain:\n", language)

	for _, status := range statuses {
		found := status.Version
		if found == "" {
			found = "-"
		}

		state := "ok"
		if !status.OK {
			state = status.Problem()
		}

		where := status.Path
		if status.Tool.Release != nil {
			where += " (installed by tt)"
		}

		fmt.Fprintf(&out, "  %-16s %-18s %-10s %-10s %s\n",
			status.Tool.Name, status.Tool.Requirement(), found, state, where)
	}

	return out.String()
}

// installTool installs the release of tool into the project, unless it is
// installed already, links it and the other programs it names into the
// project's bin directory, and returns the program.
func installTool(ctx context.Context, tool Tool, opts ToolchainOptions) (string, error) {
	release := tool.Release

	var (
		program string
		err     error
	)

	switch {
	case release.Wheel:
		program, err = installWheelTool(ctx, tool, opts)
	case release.Tree != "":
		program, err = installTreeTool(ctx, tool, opts)
	default:
		program, err = installAssetTool(ctx, tool, opts)
	}

	if err != nil {
		return "", err
	}

	// A tree reports the version of what it holds - the clang of a wasi-sdk
	// release, say - rather than the version of the release.
	got := toolVersion(ctx, program, tool.VersionArgs)
	if got == "" || release.Tree == "" && got != release.Version ||
		release.Tree != "" && !versionFits(got, tool.Minimum, tool.Below) {
		return "", fmt.Errorf("%w: %s reports %q, want %s", errToolVersion, program, got,
			tool.Requirement())
	}

	bin := filepath.Join(opts.Root, "bin")

	for _, link := range release.Links {
		err = relink(filepath.Join(bin, path.Base(link)),
			filepath.Join(toolDir(opts.Root, tool), filepath.FromSlash(link)))
		if err != nil {
			return "", err
		}
	}

	if release.Unlinked {
		return program, nil
	}

	return program, relink(filepath.Join(bin, tool.Name), program)
}

// toolDir is the version directory of the release of tool.
func toolDir(root string, tool Tool) string {
	name := tool.Release.Dir
	if name == "" {
		name = tool.Name
	}

	return filepath.Join(root, "versions", name, name+"_"+tool.Release.Version)
}

// installTreeTool unpacks the archive of a tool whole, and the archives laid
// over it, into its version directory, unless the program is there, and
// returns the program. The tree is unpacked aside and renamed into place, so
// a failed installation leaves none behind.
func installTreeTool(ctx context.Context, tool Tool, opts ToolchainOptions) (string, error) {
	dir := toolDir(opts.Root, tool)
	program := filepath.Join(dir, filepath.FromSlash(tool.Release.Tree))

	if isExecutable(program) {
		return program, nil
	}

	asset, ok := tool.Release.Assets[hostKey()]
	if !ok {
		return "", fmt.Errorf("%w: %s", errNoAsset, hostKey())
	}

	staging := dir + ".staging"

	err := os.RemoveAll(staging)
	if err != nil {
		return "", fmt.Errorf("clearing %s: %w", staging, err)
	}

	defer func() { _ = os.RemoveAll(staging) }()

	for _, archive := range append([]ToolAsset{asset}, tool.Release.Overlays[hostKey()]...) {
		data, err := opts.Fetcher.FetchURL(ctx, archive.URL, archive.SHA256)
		if err != nil {
			return "", err
		}

		err = unpackTree(data, staging)
		if err != nil {
			return "", fmt.Errorf("unpacking %s: %w", path.Base(archive.URL), err)
		}
	}

	if !isExecutable(filepath.Join(staging, filepath.FromSlash(tool.Release.Tree))) {
		return "", fmt.Errorf("%w: %s", errNoMember, tool.Release.Tree)
	}

	err = os.RemoveAll(dir)
	if err == nil {
		err = os.Rename(staging, dir)
	}

	if err != nil {
		return "", fmt.Errorf("installing %s: %w", dir, err)
	}

	return program, nil
}

// installWheelTool installs the wheel of a tool into the environment of
// tarawasm, unless the version there is the release, and returns the program.
// tt downloads the wheel, checked against its sha256 and cached as any asset,
// and pip installs that file: no index is asked, so a slow route to PyPI does
// not stall the installation.
func installWheelTool(ctx context.Context, tool Tool, opts ToolchainOptions) (string, error) {
	if opts.TarawasmEnv == "" {
		return "", fmt.Errorf("%w: pip installs %s into the environment of tarawasm",
			ErrNoTarawasm, tool.Name)
	}

	program := filepath.Join(opts.TarawasmEnv, "bin", tool.Name)
	if toolVersion(ctx, program, tool.VersionArgs) == tool.Release.Version {
		return program, nil
	}

	asset, ok := tool.Release.Assets[hostKey()]
	if !ok {
		return "", fmt.Errorf("%w: %s", errNoAsset, hostKey())
	}

	name := path.Base(asset.URL)

	data, err := opts.Fetcher.FetchURL(ctx, asset.URL, asset.SHA256)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "tt-wasm-wheel-")
	if err != nil {
		return "", fmt.Errorf("staging the wheel: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	// pip reads the platform of a wheel from its file name.
	wheel := filepath.Join(dir, name)

	err = os.WriteFile(wheel, data, filePerm)
	if err != nil {
		return "", fmt.Errorf("staging the wheel: %w", err)
	}

	runner := Tarawasm{Python: "", Source: "", Reinstall: false, Output: opts.Output, binDir: ""}

	err = runner.run(ctx, filepath.Join(opts.TarawasmEnv, "bin", "python"), "-m", "pip",
		"install", "--disable-pip-version-check", "--no-input", "--no-index", "--no-deps",
		wheel)
	if err != nil {
		return "", err
	}

	return program, nil
}

// installAssetTool installs a downloaded tool under versions/ of the project,
// unless it is there, and returns the program.
func installAssetTool(ctx context.Context, tool Tool, opts ToolchainOptions) (string, error) {
	program := filepath.Join(toolDir(opts.Root, tool), tool.Name)

	if isExecutable(program) {
		return program, nil
	}

	return program, fetchTool(ctx, tool, opts.Fetcher, program)
}

// fetchTool downloads the asset of the host, checks it and writes the program
// it is, or holds, to program.
func fetchTool(ctx context.Context, tool Tool, fetcher Fetcher, program string) error {
	asset, ok := tool.Release.Assets[hostKey()]
	if !ok {
		return fmt.Errorf("%w: %s", errNoAsset, hostKey())
	}

	data, err := fetcher.FetchURL(ctx, asset.URL, asset.SHA256)
	if err != nil {
		return err
	}

	if strings.HasSuffix(asset.URL, ".tar.gz") {
		// The program is the file of its name, at the top of the archive or
		// in the directory of the release.
		data, err = fetch.UnpackFile(data, tool.Name, maxAssetSize)
		if err != nil {
			return fmt.Errorf("%s: %w", path.Base(asset.URL), err)
		}
	}

	err = os.MkdirAll(filepath.Dir(program), dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(program), err)
	}

	err = writeFileAtomic(program, data)
	if err != nil {
		return err
	}

	err = os.Chmod(program, toolPerm)
	if err != nil {
		return fmt.Errorf("making %s executable: %w", program, err)
	}

	return nil
}
