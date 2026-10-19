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

const projectScriptPermissions = 0o644

//go:embed templates/activate.sh
var projectActivateScript string

//go:embed templates/deactivate.sh
var projectDeactivateScript string

// prepareProjectEnvironment creates missing shell scripts before activation.
// Existing scripts belong to the user and are preserved.
func prepareProjectEnvironment(scope binary.Scope) error {
	if scope != binary.ScopeProject {
		return nil
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to resolve project directory: %w", err)
	}

	directory := filepath.Join(projectDir, ".env")

	activatePath := filepath.Join(directory, "activate.sh")
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
