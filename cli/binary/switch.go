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

// ansi is a string to clean strings from ANSI escape codes using regexp.
const ansi = "[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;" +
	"[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))"

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
		return "", fmt.Errorf("there are no binaries installed in this environment of 'tt'")
	} else if err != nil {
		return "", fmt.Errorf("error reading directory %q: %s", binDir, err)
	}
	versions, err := ParseBinaries(binDirFilesList, program, binDir)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("there are no %s installed in this environment of 'tt'", program)
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
	if strings.HasSuffix(version, " [active]") {
		version = strings.TrimSuffix(version, " [active]")
	}

	return version, err
}

// switchTt switches 'tt' program.
func switchTt(switchCtx SwitchCtx) error {
	log.Infof("Switching to %s %s.", switchCtx.Program, switchCtx.Version)

	ttVersion := switchCtx.Version
	if !strings.HasPrefix(switchCtx.Version, "v") {
		ttVersion = "v" + ttVersion
	}
	versionStr := search.ProgramTt.String() + version.FsSeparator + ttVersion

	if util.IsRegularFile(filepath.Join(switchCtx.BinDir, versionStr)) {
		err := util.CreateSymlink(versionStr, filepath.Join(switchCtx.BinDir, "tt"), true)
		if err != nil {
			return fmt.Errorf("failed to switch version: %s", err)
		}
		log.Infof("Done")
	} else {
		return fmt.Errorf("%s %s is not installed in current environment",
			switchCtx.Program, switchCtx.Version)
	}
	return nil
}

// switchTarantool switches 'tarantool' program.
func switchTarantool(switchCtx SwitchCtx, enterprise bool) error {
	log.Infof("Switching to %s %s.", switchCtx.Program, switchCtx.Version)
	var versionStr string
	if enterprise {
		versionStr = search.ProgramEe.String() + version.FsSeparator + switchCtx.Version
	} else {
		versionStr = search.ProgramCe.String() + version.FsSeparator + switchCtx.Version
	}
	if util.IsRegularFile(filepath.Join(switchCtx.BinDir, versionStr)) &&
		util.IsDir(filepath.Join(switchCtx.IncDir, "include", versionStr)) {
		err := util.CreateSymlink(versionStr, filepath.Join(switchCtx.BinDir,
			"tarantool"), true)
		if err != nil {
			return fmt.Errorf("failed to switch version: %s", err)
		}
		err = util.CreateSymlink(versionStr, filepath.Join(switchCtx.IncDir,
			"include", "tarantool"), true)
		if err != nil {
			return fmt.Errorf("failed to switch version: %s", err)
		}
		log.Infof("Done")
	} else {
		return fmt.Errorf("%s %s is not installed in current environment",
			switchCtx.Program, switchCtx.Version)
	}
	return nil
}

// Switch switches binaries.
func Switch(switchCtx SwitchCtx) error {
	var err error

	switch switchCtx.Program {
	case search.ProgramTt:
		err = switchTt(switchCtx)
	case search.ProgramCe:
		err = switchTarantool(switchCtx, false)
	case search.ProgramEe:
		err = switchTarantool(switchCtx, true)
	default:
		return fmt.Errorf("unknown application: %s", switchCtx.Program)
	}

	return err
}
