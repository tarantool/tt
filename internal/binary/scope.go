package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/scope"
)

// Scope selects the installation scope.
type Scope int

const (
	// ScopeUnknown means no scope was selected.
	ScopeUnknown Scope = iota
	// ScopeProject manages binaries in the project the current directory is
	// in, or in the current directory outside a project.
	ScopeProject
	// ScopeUser manages binaries for the current user.
	ScopeUser
	// ScopeSystem manages binaries for the whole system.
	ScopeSystem
)

const (
	// VersionsDirName is the directory containing installations grouped by product.
	VersionsDirName = "versions"
	// BinDirName is the directory for active executable links within a scope.
	BinDirName = "bin"
	// IncDirName is the headers directory within a scope or a version.
	IncDirName = "include"
)

var scopeToString = map[Scope]string{
	ScopeProject: "project",
	ScopeUser:    "user",
	ScopeSystem:  "system",
}

// ErrUnknownScope reports an unsupported scope name.
var ErrUnknownScope = errors.New("unknown scope")

// String returns the scope name.
func (s Scope) String() string {
	if name, ok := scopeToString[s]; ok {
		return name
	}

	return fmt.Sprintf("unknown(%d)", s)
}

// ParseScope converts a scope name to Scope.
func ParseScope(value string) (Scope, error) {
	for candidate, name := range scopeToString {
		if name == value {
			return candidate, nil
		}
	}

	return ScopeUnknown, fmt.Errorf("%w %q: use project, user or system", ErrUnknownScope, value)
}

// ArtifactPath returns the absolute artifact root for the scope without
// creating it. The roots are shared with the other command groups that work
// in a scope, so internal/scope resolves them.
func (s Scope) ArtifactPath() (string, error) {
	switch s {
	case ScopeProject:
		projectDir, err := ProjectDir()
		if err != nil {
			return "", err
		}

		return scope.ProjectRoot(projectDir), nil
	case ScopeUser:
		return scope.UserRoot()
	case ScopeSystem:
		return scope.SystemRoot()
	default:
		return "", fmt.Errorf("%w: %d", ErrUnknownScope, s)
	}
}

// ProjectDir returns the directory whose .tarantool is the root of the
// project scope: the project the working directory is in, or the working
// directory itself when it is in none.
//
// Falling back to the working directory keeps tt binary install usable in a
// directory that is not a project yet, so the binaries can come before the
// manifest. tt binary has no -C, so the search always starts from the
// working directory. Inside an installed version (scope.InstalledTree) there
// is no fallback: the project scope there would write into the version, so
// ProjectDir fails with scope.ErrInstalledTree and points at the other scopes.
func ProjectDir() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot resolve project artifact path: %w", err)
	}

	projectDir, found, err := scope.FindProject(workingDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve project artifact path: %w", err)
	}

	if found {
		return projectDir, nil
	}

	// Inside an installed version there is no project to fall back on: the
	// working directory would put .tarantool into the version itself.
	installed, err := scope.InstalledTree(workingDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve project artifact path: %w", err)
	}

	if installed {
		return "", fmt.Errorf("cannot resolve project artifact path: %s %w; "+
			"use --scope user or --scope system", workingDir, scope.ErrInstalledTree)
	}

	return workingDir, nil
}

// InstallationName returns the directory name of a standalone or SDK component.
func InstallationName(program Program, selected string) string {
	if sdkVersion, found := SDKComponentVersion(program, selected); found {
		return program.Exec() + "_sdk_" + sdkVersion
	}

	return program.String() + version.FsSeparator + selected
}

// InstallationVersion decodes a version directory into its CLI identifier.
func InstallationVersion(program Program, name string) (string, bool) {
	if program.CanBeSDKComponent() {
		if selected, found := strings.CutPrefix(name, program.Exec()+"_sdk_"); found {
			return SDKVersionPrefix + selected, selected != ""
		}
	}

	selected, found := strings.CutPrefix(name, program.String()+version.FsSeparator)

	return selected, found && selected != ""
}

// InstallationPath locates a version in the selected scope.
func InstallationPath(root string, program Program, selected string) string {
	return filepath.Join(root, VersionsDirName, program.String(),
		InstallationName(program, selected))
}

// InstalledVersions lists version directories, including incomplete installations.
func InstalledVersions(root string, program Program) ([]string, error) {
	directory := filepath.Join(root, VersionsDirName, program.String())
	entries, err := os.ReadDir(directory)

	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read directory %q: %w", directory, err)
	}

	var installed []string

	for _, entry := range entries {
		selected, found := InstallationVersion(program, entry.Name())
		if entry.IsDir() && found {
			installed = append(installed, selected)
		}
	}

	return installed, nil
}
