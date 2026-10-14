package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/internal/binary"
)

const projectScriptPermissions = 0o644

const projectActivateScript = `#!/bin/sh
if [ "${TT_USER_OLD_PATH+x}" != x ]; then
    export TT_USER_OLD_PATH="${PATH-}"
    export TT_USER_OLD_TARANTOOL_PREFIX_SET="${TT_CLI_TARANTOOL_PREFIX+x}"
    export TT_USER_OLD_TARANTOOL_PREFIX="${TT_CLI_TARANTOOL_PREFIX-}"
fi

export TT_PROJECT_DIR=%s
export PATH="$TT_PROJECT_DIR/.tarantool/bin:$TT_USER_OLD_PATH"
export TT_CLI_TARANTOOL_PREFIX="$TT_PROJECT_DIR/.tarantool"
`

const projectDeactivateScript = `#!/bin/sh
if [ "${TT_USER_OLD_PATH+x}" = x ]; then
    export PATH="$TT_USER_OLD_PATH"
fi

if [ "${TT_USER_OLD_TARANTOOL_PREFIX_SET-}" = x ]; then
    export TT_CLI_TARANTOOL_PREFIX="${TT_USER_OLD_TARANTOOL_PREFIX-}"
elif [ "${TT_USER_OLD_TARANTOOL_PREFIX_SET+x}" = x ]; then
    unset TT_CLI_TARANTOOL_PREFIX
fi

unset TT_USER_OLD_PATH TT_USER_OLD_TARANTOOL_PREFIX TT_USER_OLD_TARANTOOL_PREFIX_SET
unset TT_PROJECT_DIR
`

// prepareProjectEnvironment creates missing shell scripts after a successful
// project installation. Existing scripts belong to the user and are preserved.
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
