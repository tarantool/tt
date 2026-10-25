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

	directory := filepath.Join(projectDir, projectEnvDirName)

	activatePath := filepath.Join(directory, projectActivateName)
	deactivatePath := filepath.Join(directory, "deactivate.sh")

	if binary.IsRegularFile(activatePath) && binary.IsRegularFile(deactivatePath) {
		return nil
	}

	err = os.MkdirAll(directory, defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create project environment directory: %w", err)
	}

	// Single quotes keep shell expansions and special characters in paths literal.
	quotedProjectDir := "'" + strings.ReplaceAll(projectDir, "'", "'\"'\"'") + "'"

	err = writeProjectScript(activatePath, fmt.Sprintf(projectActivateScript, quotedProjectDir))
	if err != nil {
		return err
	}

	err = writeProjectScript(deactivatePath, projectDeactivateScript)
	if err != nil {
		return err
	}

	return nil
}

// projectActivateCommand returns the command that activates the project
// binaries, as typed in the working directory: "source .env/activate.sh" in
// the project directory and "source ../.env/activate.sh" one level below.
// The path climbs with ".." instead of naming the project directory, so it
// needs no quoting whatever that directory is called.
//
// It is printed after a successful installation, when the project directory
// has just been resolved. Should it fail now, the command is spelled as from
// the project directory: a hint is not worth failing the installation for.
func projectActivateCommand() string {
	activatePath := filepath.Join(projectEnvDirName, projectActivateName)

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
