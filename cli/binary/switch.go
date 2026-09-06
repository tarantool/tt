package binary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apex/log"
	"github.com/fatih/color"
	"github.com/manifoldco/promptui"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

var (
	errBinaryIsNotInstalledInCurrentEnvironment  = errors.New("binary ")
	errHeadersIsNotInstalledInCurrentEnvironment = errors.New("headers ")
	errThereAreNoInstalledInThisEnvironmentOfTT  = errors.New("there are no ")
	errUnknownApplication                        = errors.New("unknown application: ")
)

// SwitchCtx contains information for switch command.
type SwitchCtx struct {
	// BinDir is a directory witch stores binaries.
	BinDir string
	// IncDir is a directory witch stores include files.
	IncDir string
	// Program is a program name to switch to.
	Program search.Program
	// Version of the program to switch to.
	Version string
}

// spell-checker:disable
// ansi is a string to clean strings from ANSI escape codes using regexp.
const ansi = "[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;" +
	"[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))"

// spell-checker:enable

func cleanString(str string) string {
	re := regexp.MustCompile(ansi)
	return re.ReplaceAllString(str, "")
}

// ChooseProgram shows a menu in terminal to choose program for switch.
func ChooseProgram(supportedPrograms []string) (search.Program, error) {
	programSelect := promptui.Select{
		Label:        "Select program",
		Items:        supportedPrograms,
		HideSelected: true,
	}

	var program string
	var err error
	if _, program, err = programSelect.Run(); err != nil {
		return search.ProgramUnknown, fmt.Errorf("failed to choose program: %w", err)
	}

	return search.ParseProgram(program)
}

// ChooseVersion shows a menu in terminal to choose version of program to switch to.
func ChooseVersion(binDir string, program search.Program) (string, error) {
	binDirFilesList, err := os.ReadDir(binDir)

	if len(binDirFilesList) == 0 || errors.Is(err, fs.ErrNotExist) {
		return "", errNoBinariesInstalled
	} else if err != nil {
		return "", fmt.Errorf("error reading directory %q: %w", binDir, err)
	}
	versions, err := ParseBinaries(binDirFilesList, program, binDir)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("%w%s installed in this environment of 'tt'",
			errThereAreNoInstalledInThisEnvironmentOfTT, program)
	}
	var versionStr []string
	for _, version := range versions {
		if strings.Contains(version.Str, "[active]") {
			versionStr = append(versionStr, util.Bold(color.GreenString(version.Str)))
			continue
		}
		versionStr = append(versionStr, color.YellowString(version.Str))
	}
	versionSelect := promptui.Select{
		Label:        "Select version",
		Items:        versionStr,
		HideSelected: true,
	}
	_, version, err := versionSelect.Run()
	version = cleanString(version)
	version = strings.TrimSuffix(version, " [active]")

	return version, err
}

// switchHeaders makes symlink for required version of headers.
func switchHeaders(switchCtx *SwitchCtx, versionStr string) error {
	includeDir := filepath.Join(switchCtx.IncDir, "include")

	if !util.IsDir(filepath.Join(includeDir, versionStr)) {
		return fmt.Errorf("%w%s is not installed in current environment",
			errHeadersIsNotInstalledInCurrentEnvironment, versionStr)
	}

	err := util.CreateSymlink(versionStr,
		filepath.Join(includeDir, switchCtx.Program.Exec()),
		true)
	if err != nil {
		return fmt.Errorf("failed create symlink: %w", err)
	}
	return nil
}

// switchBinary makes symlink for required binary version.
func switchBinary(switchCtx *SwitchCtx, versionStr string) error {
	newBinary := filepath.Join(switchCtx.BinDir, versionStr)
	if !util.IsRegularFile(newBinary) {
		return fmt.Errorf("%w%s is not installed in current environment",
			errBinaryIsNotInstalledInCurrentEnvironment, newBinary)
	}

	err := util.CreateSymlink(versionStr,
		filepath.Join(switchCtx.BinDir, switchCtx.Program.Exec()),
		true)
	if err != nil {
		return fmt.Errorf("failed create symlink: %w", err)
	}
	return nil
}

// Switch switches binaries.
func Switch(switchCtx *SwitchCtx) error {
	switch switchCtx.Program {
	case search.ProgramTt:
		if !strings.HasPrefix(switchCtx.Version, "v") {
			switchCtx.Version = "v" + switchCtx.Version
		}

	case search.ProgramUnknown:
		return fmt.Errorf("%w%s", errUnknownApplication, switchCtx.Program)
	case search.ProgramCe, search.ProgramEe, search.ProgramDev, search.ProgramTcm:
		// These programs do not require tt version normalization.
	}

	versionStr := switchCtx.Program.String() + version.FsSeparator + switchCtx.Version
	log.Infof("Switching to %s", versionStr)

	err := switchBinary(switchCtx, versionStr)
	if err != nil {
		return fmt.Errorf("failed to switch binary: %w", err)
	}

	if switchCtx.Program.IsTarantool() {
		err = switchHeaders(switchCtx, versionStr)
		if err != nil {
			return fmt.Errorf("failed to switch headers: %w", err)
		}
	}

	log.Infof("Done")
	return nil
}
