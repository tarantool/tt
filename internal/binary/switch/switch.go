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
	"golang.org/x/term"
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

// Choice is an installed version tt binary switch can select.
type Choice struct {
	// Selection is the product and the installed version.
	Selection binary.Selection
	// Argument is the version as tt binary switch <program> takes it for
	// the program the choices were listed for: 3.8.1ee for a Tarantool
	// Enterprise 3.8.1 listed for tarantool.
	Argument string
	// Active tells the version the active links select.
	Active bool
}

// Choices lists the versions of program installed in the scope; for
// tarantool and tt, the versions of their Enterprise Edition follow, as
// tt binary switch tarantool selects either.
func Choices(scope binary.Scope, program binary.Program) ([]Choice, error) {
	err := validateProgram(program)
	if err != nil {
		return nil, err
	}

	root, err := scope.ArtifactPath()
	if err != nil {
		return nil, err
	}

	programs := []binary.Program{program}
	if enterprise := program.EnterpriseProgram(); enterprise != binary.ProgramUnknown {
		programs = append(programs, enterprise)
	}

	var choices []Choice

	for _, edition := range programs {
		directory := filepath.Join(root, binary.VersionsDirName, edition.String())
		entries, err := os.ReadDir(directory)

		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("error reading directory %q: %w", directory, err)
		}

		versions, err := list.ParseBinaries(entries, edition, root)
		if err != nil {
			return nil, err
		}

		for _, installed := range versions {
			selected, active := strings.CutSuffix(installed.Str, " [active]")

			argument := selected
			if edition != program {
				argument = binary.VersionArgument(edition, selected)
			}

			choices = append(choices, Choice{
				Selection: binary.Selection{Program: edition, ID: selected},
				Argument:  argument,
				Active:    active,
			})
		}
	}

	return choices, nil
}

// ErrVersionRequired reports a switch with no version where no menu can be
// shown: stdin or stdout is not a terminal.
var ErrVersionRequired = errors.New("a version is required off a terminal")

// VersionRequired is the error of a switch of program with no version off a
// terminal: it names the command to run and the versions installed in the
// scope. With no program it names the products that have some.
func VersionRequired(scope binary.Scope, program binary.Program, programs []string) error {
	if program == binary.ProgramUnknown {
		var installed []string

		for _, name := range programs {
			product, err := binary.ParseProgram(name)
			if err != nil {
				return err
			}

			choices, err := Choices(scope, product)
			if err != nil {
				return err
			}

			if len(choices) > 0 {
				installed = append(installed, name+" "+joinArguments(choices))
			}
		}

		if len(installed) == 0 {
			return fmt.Errorf("%w: tt binary switch <program> <version>; "+
				"nothing is installed in the selected scope", ErrVersionRequired)
		}

		return fmt.Errorf("%w: tt binary switch <program> <version>; installed: %s",
			ErrVersionRequired, strings.Join(installed, "; "))
	}

	choices, err := Choices(scope, program)
	if err != nil {
		return err
	}

	if len(choices) == 0 {
		return fmt.Errorf("%w%s versions installed in the selected scope",
			errNoInstalledVersionsInScope, program)
	}

	return fmt.Errorf("%w: tt binary switch %s <version>; installed: %s",
		ErrVersionRequired, program, joinArguments(choices))
}

// joinArguments lists the versions of choices as switch takes them, the
// active one marked.
func joinArguments(choices []Choice) string {
	arguments := make([]string, 0, len(choices))

	for _, choice := range choices {
		argument := choice.Argument
		if choice.Active {
			argument += " (active)"
		}

		arguments = append(arguments, argument)
	}

	return strings.Join(arguments, ", ")
}

// ChooseInstallation shows a menu of the versions of program installed in
// the scope and returns the one chosen. For tarantool and tt the menu has
// the versions of both editions, Enterprise ones as 3.8.1ee: the product is
// named, so no edition is asked for.
func ChooseInstallation(scope binary.Scope, program binary.Program) (binary.Selection, error) {
	choices, err := Choices(scope, program)
	if err != nil {
		return binary.Selection{}, err
	}

	if len(choices) == 0 {
		return binary.Selection{}, fmt.Errorf("%w%s versions installed in the selected scope",
			errNoInstalledVersionsInScope, program)
	}

	cursor := 0

	for i, choice := range choices {
		if choice.Active {
			cursor = i
		}
	}

	index, err := selectVersion("Select "+program.String()+" version", choiceLabels(choices),
		cursor)
	if err != nil {
		return binary.Selection{}, err
	}

	return choices[index].Selection, nil
}

// Interactive reports whether a menu can be shown: stdin, which the menu
// reads, and stdout, which it draws on, are terminals. A variable for the
// tests.
var Interactive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// ChooseFrom shows the version menu of tt binary switch for versions other
// than those of a program of tt binary, such as the installed versions of an
// application tt package switch offers: active, if it is one of them, is
// marked and selected first. Off a terminal it fails with ErrVersionRequired.
func ChooseFrom(label string, versions []string, active string) (string, error) {
	choices := make([]string, 0, len(versions))
	cursor := 0

	for i, installed := range versions {
		if installed == active {
			choices = append(choices, binary.Bold(color.GreenString(installed+" [active]")))
			cursor = i

			continue
		}

		choices = append(choices, color.YellowString(installed))
	}

	index, err := selectVersion(label, choices, cursor)
	if err != nil {
		return "", err
	}

	return versions[index], nil
}

// selectVersion shows a menu of choices with the cursor at cursor and returns
// the index of the chosen one. Off a terminal (Interactive) it shows nothing
// and fails with ErrVersionRequired: a menu drawn into a pipe has no one to
// answer it.
func selectVersion(label string, choices []string, cursor int) (int, error) {
	if !Interactive() {
		return 0, fmt.Errorf("%w: %s", ErrVersionRequired, strings.ToLower(label))
	}

	versionSelect := promptui.Select{
		Label:        label,
		Items:        choices,
		CursorPos:    cursor,
		HideSelected: true,
	}

	index, _, err := versionSelect.Run()
	if err != nil {
		return 0, fmt.Errorf("failed to choose version: %w", err)
	}

	return index, nil
}

// choiceLabels formats menu labels without changing installation identities.
func choiceLabels(choices []Choice) []string {
	labels := make([]string, 0, len(choices))

	for _, choice := range choices {
		label := strings.TrimPrefix(choice.Argument, "v")
		if choice.Active {
			labels = append(labels, binary.Bold(color.GreenString(label+" [active]")))

			continue
		}

		labels = append(labels, color.YellowString(label))
	}

	return labels
}

func chooseProgram(label string, supportedPrograms []string) (binary.Program, error) {
	if !Interactive() {
		return binary.ProgramUnknown, fmt.Errorf("%w: %s", ErrVersionRequired,
			strings.ToLower(label))
	}

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
