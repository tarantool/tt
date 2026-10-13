package switcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/manifoldco/promptui"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/list"
)

// activeDirectoryMode makes system-scope selections accessible to every user.
const activeDirectoryMode = 0o755

var (
	errBinaryIsNotInstalledInCurrentEnvironment  = errors.New("binary ")
	errHeadersIsNotInstalledInCurrentEnvironment = errors.New("headers ")
	errThereAreNoInstalledInThisEnvironmentOfTT  = errors.New("there are no ")
	errInvalidVersion                            = errors.New("invalid version")
)

// SwitchCtx contains information for switch command.
type SwitchCtx struct {
	binary.BinaryContext

	// Version of the program to switch to.
	Version string

	// ExactVersion preserves the identity of an installation selected from the menu.
	ExactVersion bool
}

// ChooseProgram shows a menu in terminal to choose program for switch.
func ChooseProgram(supportedPrograms []string) (binary.Program, error) {
	programSelect := promptui.Select{
		Label:        "Select program",
		Items:        supportedPrograms,
		HideSelected: true,
	}

	var (
		program string
		err     error
	)

	_, program, err = programSelect.Run()
	if err != nil {
		return binary.ProgramUnknown, fmt.Errorf("failed to choose program: %w", err)
	}

	return binary.ParseProgram(program)
}

// versionChoices formats menu labels without changing installation identities.
func versionChoices(versions []version.Version) []string {
	choices := make([]string, 0, len(versions))
	for _, installed := range versions {
		label := strings.TrimPrefix(installed.Str, "v")
		if strings.HasSuffix(installed.Str, " [active]") {
			choices = append(choices, binary.Bold(color.GreenString(label)))
			continue
		}

		choices = append(choices, color.YellowString(label))
	}

	return choices
}

// ChooseVersion shows a menu of versions installed in the selected scope.
func ChooseVersion(scope binary.Scope, program binary.Program) (string, error) {
	err := validateProgram(program)
	if err != nil {
		return "", err
	}

	root, err := scope.ArtifactPath()
	if err != nil {
		return "", err
	}

	directory := filepath.Join(root, binary.VersionsDirName, program.String())
	entries, err := os.ReadDir(directory)

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("error reading directory %q: %w", directory, err)
	}

	versions, err := list.ParseBinaries(entries, program, root)
	if err != nil {
		return "", err
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("%w%s installed in this environment of 'tt'",
			errThereAreNoInstalledInThisEnvironmentOfTT, program)
	}

	versionSelect := promptui.Select{
		Label:        "Select version",
		Items:        versionChoices(versions),
		HideSelected: true,
	}

	index, _, err := versionSelect.Run()
	if err != nil {
		return "", fmt.Errorf("failed to choose version: %w", err)
	}

	return strings.TrimSuffix(versions[index].Str, " [active]"), nil
}

// validateProgram limits switching to products with installed executables.
func validateProgram(program binary.Program) error {
	switch program {
	case binary.ProgramTt, binary.ProgramCe, binary.ProgramEe, binary.ProgramTcm:
		return nil
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, program)
	}
}

// resolveInstalledBundleVersion examines only installation names in the selected scope.
func resolveInstalledBundleVersion(root string, program binary.Program,
	requested string,
) (string, error) {
	directory := filepath.Join(root, binary.VersionsDirName, program.String())
	entries, err := os.ReadDir(directory)

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("error reading directory %q: %w", directory, err)
	}

	installed := make([]string, 0, len(entries))
	prefix := program.String() + version.FsSeparator

	for _, entry := range entries {
		name, ok := strings.CutPrefix(entry.Name(), prefix)
		if entry.IsDir() && ok {
			installed = append(installed, name)
		}
	}

	return version.MatchInstalledVersion(requested, installed)
}

// findInstalledVersion resolves an installed bundle version or an exact Git version.
func findInstalledVersion(root string, program binary.Program,
	requested string, exactVersion bool,
) (string, string, error) {
	if requested == "" || requested == "." || requested == ".." ||
		filepath.IsAbs(requested) || strings.ContainsAny(requested, "/\\\x00") {
		return "", "", fmt.Errorf("%w: %q", errInvalidVersion, requested)
	}

	candidates := []string{requested}
	if !exactVersion && (program == binary.ProgramEe || program == binary.ProgramTcm) {
		selected, err := resolveInstalledBundleVersion(root, program, requested)

		switch {
		case errors.Is(err, version.ErrInstalledVersionNotFound):
			candidates = nil
		case err != nil:
			return "", "", err
		default:
			candidates = []string{selected}
		}
	} else if alternative, ok := alternateReleaseSpelling(requested); !exactVersion && ok {
		candidates = append(candidates, alternative)
	}

	for _, candidate := range candidates {
		installation := filepath.Join(root, binary.VersionsDirName, program.String(),
			program.String()+version.FsSeparator+candidate)
		binPath := filepath.Join(installation, program.Exec())
		info, err := os.Stat(binPath)

		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return "", "", fmt.Errorf("failed to access %q: %w", binPath, err)
		case info.Mode().IsRegular():
			return candidate, installation, nil
		}
	}

	return "", "", fmt.Errorf("%w%s is not installed in current environment",
		errBinaryIsNotInstalledInCurrentEnvironment,
		program.String()+version.FsSeparator+requested)
}

// alternateReleaseSpelling accepts v-prefixed release tags without altering branches or hashes.
func alternateReleaseSpelling(requested string) (string, bool) {
	_, err := version.Parse(requested)
	if err != nil {
		return "", false
	}

	if release, found := strings.CutPrefix(requested, "v"); found {
		return release, true
	}

	return "v" + requested, true
}

// switchLink prepares the active directory and replaces one link in the selected scope.
func switchLink(target, link string) error {
	err := binary.CreateDirectory(filepath.Dir(link), activeDirectoryMode)
	if err != nil {
		return fmt.Errorf("failed to create active directory: %w", err)
	}

	err = binary.CreateSymlink(target, link, true)
	if err != nil {
		return fmt.Errorf("failed create symlink: %w", err)
	}

	return nil
}

// Switch selects an installed version in exactly the requested scope.
func Switch(switchCtx *SwitchCtx) error {
	err := validateProgram(switchCtx.Program)
	if err != nil {
		return err
	}

	root, err := switchCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	selected, installation, err := findInstalledVersion(root, switchCtx.Program,
		switchCtx.Version, switchCtx.ExactVersion)
	if err != nil {
		return fmt.Errorf("failed to switch binary: %w", err)
	}

	log.Infof("Switching to %s%s%s", switchCtx.Program, version.FsSeparator, selected)

	binPath := filepath.Join(installation, switchCtx.Program.Exec())
	incPath := filepath.Join(installation, binary.IncDirName, switchCtx.Program.Exec())

	if switchCtx.Program.IsTarantool() && !binary.IsDir(incPath) {
		return fmt.Errorf("failed to switch headers: %w%s is not installed in current environment",
			errHeadersIsNotInstalledInCurrentEnvironment,
			switchCtx.Program.String()+version.FsSeparator+selected)
	}

	binLink := filepath.Join(root, binary.BinDirName, switchCtx.Program.Exec())

	err = switchLink(binPath, binLink)
	if err != nil {
		return fmt.Errorf("failed to switch binary: %w", err)
	}

	switch switchCtx.Program {
	case binary.ProgramCe, binary.ProgramEe:
		incLink := filepath.Join(root, binary.IncDirName, switchCtx.Program.Exec())

		err = switchLink(incPath, incLink)
		if err != nil {
			return fmt.Errorf("failed to switch headers: %w", err)
		}
	default:
		// TT and TCM have no headers to activate.
	}

	log.Infof("Done")

	return nil
}
