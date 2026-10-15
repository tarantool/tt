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

var (
	errBinaryNotInstalledInScope  = errors.New("binary ")
	errHeadersNotInstalledInScope = errors.New("headers ")
	errNoInstalledVersionsInScope = errors.New("there are no ")
	errInvalidVersion             = errors.New("invalid version")
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
	return chooseProgram("Select program", supportedPrograms)
}

// ChooseInstallation selects a concrete installed version. Unified product entries
// first select an edition, so their version menus stay edition-specific.
func ChooseInstallation(scope binary.Scope, program binary.Program) (binary.Selection, error) {
	var err error

	if enterprise := program.EnterpriseProgram(); enterprise != binary.ProgramUnknown {
		program, err = chooseProgram("Select "+program.String()+" edition", []string{
			program.String(), enterprise.String(),
		})
		if err != nil {
			return binary.Selection{}, err
		}
	}

	selected, err := ChooseVersion(scope, program)
	if err != nil {
		return binary.Selection{}, err
	}

	return binary.Selection{Program: program, ID: selected}, nil
}

func chooseProgram(label string, supportedPrograms []string) (binary.Program, error) {
	programSelect := promptui.Select{
		Label:        label,
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
		return "", fmt.Errorf("%w%s versions installed in the selected scope",
			errNoInstalledVersionsInScope, program)
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
	case binary.ProgramTt, binary.ProgramTtEe, binary.ProgramCe, binary.ProgramEe,
		binary.ProgramTcm, binary.ProgramSDK:
		return nil
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, program)
	}
}

// resolveInstalledBundleVersion examines only installation names in the selected scope.
func resolveInstalledBundleVersion(root string, program binary.Program,
	requested string,
) (string, error) {
	installed, err := binary.InstalledVersions(root, program)
	if err != nil {
		return "", err
	}

	selected, err := binary.MatchInstalledVersion(program, requested, installed)

	return selected, err
}

// findInstalledVersion resolves an installed bundle version or an exact Git version.
func findInstalledVersion(root string, program binary.Program,
	requested string, exactVersion bool,
) (string, string, error) {
	if requested == "" || requested == "." || requested == ".." ||
		filepath.IsAbs(requested) || strings.ContainsAny(requested, "/\\\x00") {
		return "", "", fmt.Errorf("%w: %q", errInvalidVersion, requested)
	}

	_, fromSDK := binary.SDKComponentVersion(program, requested)
	matchBundleVersion := fromSDK || program.BundleSource() != binary.ProgramUnknown

	candidates := []string{requested}
	if !exactVersion && matchBundleVersion {
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
		installation := binary.InstallationPath(root, program, candidate)
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

	return "", "", fmt.Errorf("%w%s is not installed in the selected scope",
		errBinaryNotInstalledInScope,
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

	if switchCtx.Program == binary.ProgramSDK {
		err = switchSDK(root, switchCtx.Version, switchCtx.ExactVersion)

		return err
	}

	selected, installation, err := findInstalledVersion(root, switchCtx.Program,
		switchCtx.Version, switchCtx.ExactVersion)
	if err != nil {
		return fmt.Errorf("failed to switch binary: %w", err)
	}

	log.Infof("Switching to %s%s%s", switchCtx.Program, version.FsSeparator, selected)

	incPath := filepath.Join(installation, binary.IncDirName, switchCtx.Program.Exec())

	if switchCtx.Program.IsTarantool() && !binary.IsDir(incPath) {
		return fmt.Errorf("failed to switch headers: %w%s is not installed in the selected scope",
			errHeadersNotInstalledInScope,
			switchCtx.Program.String()+version.FsSeparator+selected)
	}

	err = binary.ActivateSelections(root,
		[]binary.Selection{{Program: switchCtx.Program, ID: selected}})
	if err != nil {
		return fmt.Errorf("failed to switch binary: %w", err)
	}

	log.Infof("Done")

	return nil
}

func switchSDK(root, requested string, exact bool) error {
	if requested == "" || requested == "." || requested == ".." ||
		strings.ContainsAny(requested, "/\\\x00") {
		return fmt.Errorf("%w: %q", errInvalidVersion, requested)
	}

	selected := requested

	if !exact {
		var err error

		selected, err = resolveInstalledBundleVersion(root, binary.ProgramSDK, requested)
		if err != nil {
			if errors.Is(err, version.ErrInstalledVersionNotFound) {
				return fmt.Errorf("%wSDK %s is not installed in the selected scope",
					errBinaryNotInstalledInScope, requested)
			}

			return err
		}
	}

	sdk, err := binary.ReadSDK(root, selected)
	if err != nil {
		return err
	}

	err = binary.ValidateSDKManifest(sdk, selected)
	if err != nil {
		return err
	}

	err = binary.ValidateSDKFiles(root, sdk)
	if err != nil {
		return err
	}

	log.Infof("Switching to SDK %s", selected)

	err = binary.ActivateSDK(root, sdk)
	if err != nil {
		return err
	}

	log.Info("Done")

	return nil
}
