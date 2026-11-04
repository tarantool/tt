package wasm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	// tarawasmCommit is the tarawasm tt installs: its Tarantool support - the
	// --target tarantool projects and their SDK lock - is on master only, the
	// 0.4.0 release on PyPI predates it. A release carrying it replaces the
	// commit with a version.
	tarawasmCommit = "bd12f414a4a54f64c231270efce78fda68efe073"
	// tarawasmArchive is the source archive of tarawasmCommit: pip builds it
	// with no git on the host.
	tarawasmArchive = "https://github.com/mandesero/tarawasm/archive/" + tarawasmCommit + ".tar.gz"
	// tarawasmProgram is the name tarawasm is linked under.
	tarawasmProgram = "tarawasm"
	// pythonCheck exits 0 under the Pythons tarawasm supports.
	pythonCheck = "import sys; sys.exit(0 if sys.version_info >= (3, 10) else 1)"
	// shortCommit is how much of the commit names the installation.
	shortCommit = 12
)

// errNotLink reports a file in the project's bin directory tt did not link.
var errNotLink = errors.New("not a link")

// Tarawasm installs tarawasm into a virtual environment of the project and
// links it into the project's bin directory.
type Tarawasm struct {
	// Python is the interpreter the environment is made with.
	Python string
	// Source is what pip installs; empty is the pinned commit.
	Source string
	// Reinstall makes the environment anew even when it is there.
	Reinstall bool
	// Output receives the output of python and pip.
	Output io.Writer

	// binDir, when set, goes in front of the PATH of the commands run.
	binDir string
}

// TarawasmLayout is where a project's tarawasm lives.
type TarawasmLayout struct {
	// Env is the virtual environment.
	Env string
	// Link is the link the project's builds find tarawasm by.
	Link string
}

// tarawasmLayout returns the layout under root, the project's .tarantool.
// The environment sits under versions/ next to the binaries tt binary
// installs, and the link in bin/ next to theirs.
func tarawasmLayout(root, source string) TarawasmLayout {
	name := tarawasmProgram + "_" + tarawasmCommit[:shortCommit]
	if source != "" {
		name = tarawasmProgram + "_custom"
	}

	return TarawasmLayout{
		Env:  filepath.Join(root, "versions", tarawasmProgram, name),
		Link: filepath.Join(root, "bin", tarawasmProgram),
	}
}

// Install installs tarawasm under root and returns where it went. An
// environment already holding tarawasm is kept unless Reinstall is set; the
// link is always pointed at it.
func (t Tarawasm) Install(ctx context.Context, root string) (TarawasmLayout, bool, error) {
	layout := tarawasmLayout(root, t.Source)
	program := filepath.Join(layout.Env, "bin", tarawasmProgram)

	installed := false

	if t.Reinstall || !isExecutable(program) {
		err := t.makeEnv(ctx, layout.Env)
		if err != nil {
			return layout, false, err
		}

		installed = true
	}

	err := relink(layout.Link, program)
	if err != nil {
		return layout, installed, err
	}

	return layout, installed, nil
}

// relink points the symbolic link at link to target through a rename, so the
// link is never missing. Anything at link that is not a symbolic link was
// put there by someone else and is refused rather than replaced.
func relink(link, target string) error {
	info, err := os.Lstat(link)
	if err == nil && info.Mode()&fs.ModeSymlink == 0 {
		return fmt.Errorf("%w: %s is there and is not a link; remove it to let tt link "+
			"tarawasm", errNotLink, link)
	}

	current, err := os.Readlink(link)
	if err == nil && current == target {
		return nil
	}

	err = os.MkdirAll(filepath.Dir(link), dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(link), err)
	}

	tmp := link + ".tmp"

	_ = os.Remove(tmp)

	err = os.Symlink(target, tmp)
	if err != nil {
		return fmt.Errorf("linking %s: %w", link, err)
	}

	err = os.Rename(tmp, link)
	if err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("linking %s: %w", link, err)
	}

	return nil
}

// isExecutable reports whether path is an executable regular file.
func isExecutable(path string) bool {
	info, err := os.Stat(path) //nolint:gosec // A path tt builds under the project.
	if err != nil {
		return false
	}

	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// UseToolchain selects the local toolchain of language for tarawasm's builds:
// tarawasm toolchain use saves it in tarawasm's user settings, refusing a
// toolchain that is not ready. EnsureToolchain has put the tools in place or
// told what to install first; tarawasm's own installation, which runs sudo
// apt-get install on Linux, is never run.
//
// program is the link in the project's bin directory, and the directory goes
// in front of tarawasm's PATH, as it does for the project's builds, after the
// directories of the programs tt installed without linking them.
func (t Tarawasm) UseToolchain(
	ctx context.Context, program, language string, unlinked map[string]string,
) error {
	t.binDir = filepath.Dir(program)

	for _, name := range sortedKeys(unlinked) {
		t.binDir += string(os.PathListSeparator) + filepath.Dir(unlinked[name])
	}

	err := t.run(ctx, program, "toolchain", "use", language, "--mode", "local")
	if err != nil {
		return fmt.Errorf("selecting the %s toolchain of tarawasm: %w", language, err)
	}

	return nil
}

// configureComponent tells tarawasm, in the settings of the component, where
// the programs tt installed without linking them are: tarawasm toolchain set
// --project writes tarawasm.local.json of the component, which belongs to
// this machine.
func (t Tarawasm) configureComponent(
	ctx context.Context, program string, component Component, unlinked map[string]string,
) error {
	t.binDir = filepath.Dir(program)

	for _, name := range sortedKeys(unlinked) {
		err := t.runIn(ctx, component.Dir, program, "toolchain", "set", component.Language,
			name, "--path", unlinked[name], "--project")
		if err != nil {
			return fmt.Errorf("telling component %q of %s: %w", component.Name, name, err)
		}
	}

	return nil
}

// makeEnv creates the virtual environment at dir and installs tarawasm in it.
// A failed attempt leaves no environment behind: a half-made one would be
// taken for a working one by the next run.
func (t Tarawasm) makeEnv(ctx context.Context, dir string) error {
	python := t.Python
	if python == "" {
		python = "python3"
	}

	path, err := exec.LookPath(python)
	if err != nil {
		return fmt.Errorf("%w: %s is not in PATH; tarawasm needs Python 3.10 or newer "+
			"(pick one with --python)", ErrPython, python)
	}

	err = exec.CommandContext(ctx, path, "-c", pythonCheck).Run()
	if err != nil {
		return fmt.Errorf("%w: %s is older than 3.10, which tarawasm needs "+
			"(pick another with --python)", ErrPython, path)
	}

	err = os.RemoveAll(dir)
	if err != nil {
		return fmt.Errorf("removing the old environment: %w", err)
	}

	err = os.MkdirAll(filepath.Dir(dir), dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(dir), err)
	}

	err = t.run(ctx, path, "-m", "venv", dir)
	if err != nil {
		_ = os.RemoveAll(dir)

		return fmt.Errorf("%w: %s cannot make a virtual environment (on Debian and "+
			"Ubuntu it needs the python3-venv package): %w", ErrPython, path, err)
	}

	source := t.Source
	if source == "" {
		source = tarawasmArchive
	}

	err = t.run(ctx, filepath.Join(dir, "bin", "python"), "-m", "pip", "install",
		"--disable-pip-version-check", "--no-input", source)
	if err != nil {
		_ = os.RemoveAll(dir)

		return fmt.Errorf("installing tarawasm from %s: %w", source, err)
	}

	return nil
}

// run runs a command with its output going to t.Output.
func (t Tarawasm) run(ctx context.Context, name string, args ...string) error {
	return t.runIn(ctx, "", name, args...)
}

// runIn is run in the directory dir; empty is the working directory.
func (t Tarawasm) runIn(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)

	cmd.Dir = dir

	output := t.Output
	if output == nil {
		output = io.Discard
	}

	cmd.Stdout = output
	cmd.Stderr = output

	if t.binDir != "" {
		cmd.Env = append(os.Environ(),
			"PATH="+t.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}

	return nil
}
