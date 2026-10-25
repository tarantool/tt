// Package scope answers where the root of a scope is, for every command group
// that installs into a scope or runs from one: tt binary, tt package and
// tt instance.
//
// A root is the directory a scope keeps its artifacts under: the installed
// versions of tarantool, tt, tcm and applications, the links selecting the
// active ones, and the runtime data of the instances started from them. The
// command groups share one root per scope, so an application installed by
// tt package finds the tarantool tt binary put next to it, and tt instance
// finds both. The answer therefore lives below all of them instead of being
// spelled out by each.
//
// The user and system roots follow the conventions of the operating system
// and are moved by TT_USER_ROOT and TT_SYSTEM_ROOT: a host that keeps
// Tarantool elsewhere sets the variable once instead of every command taking
// a flag. The project root is .tarantool in the project directory, and the
// project directory is found rather than configured: it is the nearest
// directory, starting from the one a command works in, that holds a project
// marker (see FindProject). A command run anywhere inside a project
// therefore works on the same tree.
package scope

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// UserRootEnv names the environment variable that overrides the user root.
	UserRootEnv = "TT_USER_ROOT"
	// SystemRootEnv names the environment variable that overrides the system
	// root.
	SystemRootEnv = "TT_SYSTEM_ROOT"
)

const (
	xdgDataHomeEnv  = "XDG_DATA_HOME"
	artifactDirName = "tarantool"
	kernelLinux     = "Linux"
	kernelDarwin    = "Darwin"
)

// userDirs holds the default user root of each supported kernel, relative to
// the home directory.
var userDirs = map[string]string{
	kernelLinux:  ".local/share/tarantool",
	kernelDarwin: "Library/Application Support/tarantool",
}

// systemDirs holds the default system root of each supported kernel.
var systemDirs = map[string]string{
	kernelLinux:  "/opt/tarantool",
	kernelDarwin: "/Library/Application Support/tarantool",
}

// ErrRelativeRoot is wrapped by the error for a relative TT_USER_ROOT or
// TT_SYSTEM_ROOT.
var ErrRelativeRoot = errors.New("must be an absolute path")

var errUnsupportedOS = errors.New(
	"artifact paths are unsupported for this operating system",
)

// UserRoot returns the absolute root of the user scope without creating it.
//
// TT_USER_ROOT names the root outright, on any operating system. Without it,
// the root on Linux is $XDG_DATA_HOME/tarantool when XDG_DATA_HOME is an
// absolute path and ~/.local/share/tarantool otherwise: the XDG Base
// Directory specification makes a relative value invalid and tells readers
// to ignore it. On macOS the root is ~/Library/Application Support/tarantool,
// where the platform keeps application data, and XDG_DATA_HOME is ignored.
func UserRoot() (string, error) {
	root, err := rootFromEnv(UserRootEnv)
	if root != "" || err != nil {
		return root, err
	}

	kernel, err := kernelName()
	if err != nil {
		return "", fmt.Errorf("cannot determine OS for artifact path: %w", err)
	}

	userDir, ok := userDirs[kernel]
	if !ok {
		return "", errUnsupportedOS
	}

	dataHome := os.Getenv(xdgDataHomeEnv)
	if kernel == kernelLinux && filepath.IsAbs(dataHome) {
		return filepath.Join(dataHome, artifactDirName), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory for artifact path: %w", err)
	}

	return filepath.Join(homeDir, userDir), nil
}

// SystemRoot returns the absolute root of the system scope without creating
// it.
//
// TT_SYSTEM_ROOT names the root outright, on any operating system. Without
// it, the root is /opt/tarantool on Linux and
// /Library/Application Support/tarantool on macOS.
func SystemRoot() (string, error) {
	root, err := rootFromEnv(SystemRootEnv)
	if root != "" || err != nil {
		return root, err
	}

	kernel, err := kernelName()
	if err != nil {
		return "", fmt.Errorf("cannot determine OS for artifact path: %w", err)
	}

	systemDir, ok := systemDirs[kernel]
	if !ok {
		return "", errUnsupportedOS
	}

	return systemDir, nil
}

// rootFromEnv returns the root the variable name sets, or an empty string
// when it is unset or empty; an empty variable counts as unset, as an empty
// XDG_DATA_HOME does.
//
// A relative value is an error rather than being skipped like a relative
// XDG_DATA_HOME: the variable exists only for tt, so whoever set it meant tt
// to use it, and falling back to the default root would install or start
// things where they did not ask. Resolving the value against the working
// directory instead would move the root with every cd.
func rootFromEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", nil
	}

	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s %w, got %q", name, ErrRelativeRoot, value)
	}

	return filepath.Clean(value), nil
}

// kernelName returns the operating system as uname -s names it.
//
// It asks uname, as binary.GetOs does for the platform tt binary downloads
// for, rather than reading runtime.GOOS: the directory tt binary installs
// into and the build it installs there cannot then disagree about the host.
func kernelName() (string, error) {
	out, err := exec.CommandContext(context.Background(), "uname", "-s").Output()
	if err != nil {
		return "", fmt.Errorf("cannot run uname -s: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}
