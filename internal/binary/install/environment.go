package install

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	projectScriptPermissions = 0o644
	projectEnvDirName        = ".env"
	projectActivateName      = "activate.sh"
)

//go:embed templates/activate.sh
var projectActivateScript string

//go:embed templates/deactivate.sh
var projectDeactivateScript string

//go:embed templates/activate.fish
var projectActivateFishScript string

//go:embed templates/deactivate.fish
var projectDeactivateFishScript string

// prepareProjectEnvironment creates missing shell scripts before activation.
// Existing scripts belong to the user and are preserved.
//
// The scripts go to the project directory, next to the .tarantool they
// activate, even when the installation runs in one of its subdirectories:
// a project has one set of binaries and one way to activate them.
func prepareProjectEnvironment(scope binary.Scope) error {
	if scope != binary.ScopeProject {
		return nil
	}

	projectDir, err := binary.ProjectDir()
	if err != nil {
		return err
	}

	return PrepareProjectEnvironmentIn(projectDir)
}

// PrepareProjectEnvironmentIn writes .env/activate.sh and deactivate.sh of
// the project in projectDir, and activate.fish and deactivate.fish for fish,
// when they are missing, as the first tt binary install in project scope
// does in the project it finds: for a command that knows its project and
// puts programs into its .tarantool/bin by other means. Scripts that are
// there are the user's, and are kept.
//
// The scripts find the project from their own path when they are sourced,
// so a moved or copied project activates its own binaries; activate.sh
// keeps projectDir for a shell that does not tell a sourced script its
// path.
func PrepareProjectEnvironmentIn(projectDir string) error {
	directory := filepath.Join(projectDir, projectEnvDirName)

	// Single quotes keep shell expansions and special characters in paths literal.
	quotedProjectDir := "'" + strings.ReplaceAll(projectDir, "'", "'\"'\"'") + "'"

	scripts := []struct{ name, text string }{
		{projectActivateName, fmt.Sprintf(projectActivateScript, quotedProjectDir)},
		{"deactivate.sh", projectDeactivateScript},
		{"activate.fish", projectActivateFishScript},
		{"deactivate.fish", projectDeactivateFishScript},
	}

	missing := false

	for _, script := range scripts {
		missing = missing || !binary.IsRegularFile(filepath.Join(directory, script.name))
	}

	if !missing {
		return nil
	}

	err := os.MkdirAll(directory, defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create project environment directory: %w", err)
	}

	for _, script := range scripts {
		err = writeProjectScript(filepath.Join(directory, script.name), script.text)
		if err != nil {
			return err
		}
	}

	return nil
}

// projectActivateCommand returns the command that activates the project
// binaries, as typed in the working directory: "source .env/activate.sh" in
// the project directory and "source ../.env/activate.sh" one level below.
// The path climbs with ".." instead of naming the project directory, so it
// needs no quoting whatever that directory is called. With SHELL naming
// fish, it is activate.fish.
//
// It is printed after a successful installation, when the project directory
// has just been resolved. Should it fail now, the command is spelled as from
// the project directory: a hint is not worth failing the installation for.
func projectActivateCommand() string {
	activatePath := filepath.Join(projectEnvDirName, projectActivateName)

	// fish sources a script of its own.
	if filepath.Base(os.Getenv("SHELL")) == "fish" {
		activatePath = filepath.Join(projectEnvDirName, "activate.fish")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "source " + activatePath
	}

	projectDir, err := binary.ProjectDir()
	if err != nil {
		return "source " + activatePath
	}

	toProject, err := filepath.Rel(workingDir, projectDir)
	if err != nil {
		return "source " + activatePath
	}

	return "source " + filepath.Join(toProject, activatePath)
}

func writeProjectScript(path, script string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, projectScriptPermissions)
	if errors.Is(err, os.ErrExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to create project environment script %q: %w", path, err)
	}

	_, err = file.WriteString(script)

	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(path)

		return fmt.Errorf("failed to write project environment script %q: %w", path, err)
	}

	return nil
}

// PrepareProjectEnvironment writes the project's .env/activate.sh and
// deactivate.sh when they are missing, as the first tt binary install in
// project scope does: for a command that puts programs into the project's
// .tarantool/bin by other means.
func PrepareProjectEnvironment() error {
	return prepareProjectEnvironment(binary.ScopeProject)
}

// ProjectActivateCommand returns the command that activates the project's
// binaries, as typed in the working directory.
func ProjectActivateCommand() string {
	return projectActivateCommand()
}
