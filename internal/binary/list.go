package binary

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/version"
)

var (
	errNoBinariesInstalled = errors.New(
		"there are no binaries installed in this environment of 'tt'",
	)
)

// printInstalledVersion outputs installed versions of the program.
func printInstalledVersion(versionString string) {
	if strings.HasSuffix(versionString, "[active]") {
		_, _ = fmt.Fprintf(os.Stdout, "	%s\n", Bold(color.GreenString(versionString)))
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "	%s\n", color.YellowString(versionString))
	}
}

// ParseBinaries seeks through fileList returning array of found versions of program.
func ParseBinaries(fileList []fs.DirEntry, program Program,
	binDir string,
) ([]version.Version, error) {
	var binaryVersions []version.Version

	symlinkName := program.Exec()

	binActive := ""
	programPath := filepath.Join(binDir, symlinkName)

	fileInfo, err := os.Lstat(programPath)
	if err == nil {
		switch {
		case program == ProgramDev &&
			fileInfo.Mode()&os.ModeSymlink == os.ModeSymlink:
			binActive, isTarantoolBinary, err := IsTarantoolDev(programPath, binDir)
			if err != nil {
				return binaryVersions, err
			}

			if isTarantoolBinary {
				var activeVersion version.Version

				activeVersion.Str = program.String() + " -> " + binActive + " [active]"
				binaryVersions = append(binaryVersions, activeVersion)
			}

			return binaryVersions, nil
		case program == ProgramCe && fileInfo.Mode()&os.ModeSymlink == 0:
			tntCli := cmdcontext.TarantoolCli{Executable: programPath}

			binaryVersion, err := tntCli.GetVersion()
			if err != nil {
				return binaryVersions, err
			}

			binaryVersion.Str += " [active]"

			binaryVersions = append(binaryVersions, binaryVersion)
		default:
			binActive, err = ResolveSymlink(programPath)
			if err != nil && !os.IsNotExist(err) {
				return binaryVersions, err
			}

			binActive = filepath.Base(binActive)
		}
	}

	versionPrefix := program.String() + version.FsSeparator

	for _, entry := range fileList {
		if !strings.HasPrefix(entry.Name(), versionPrefix) {
			continue
		}

		versionStr := strings.TrimPrefix(strings.TrimPrefix(entry.Name(), versionPrefix), "v")

		var ver version.Version

		isRightFormat, _ := IsValidCommitHash(versionStr)

		if versionStr == "master" {
			ver.Major = math.MaxUint // Small hack to make master the newest version.
		} else if !isRightFormat {
			ver, err = version.Parse(versionStr)
			if err != nil {
				return binaryVersions, err
			}
		}

		if binActive == entry.Name() {
			ver.Str = versionStr + " [active]"
		} else {
			ver.Str = versionStr
		}

		binaryVersions = append(binaryVersions, ver)
	}

	return binaryVersions, nil
}

// ListBinaries outputs installed versions of programs from bin_dir.
func ListBinaries(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) error {
	binDir := cliOpts.Env.BinDir
	binDirFilesList, err := os.ReadDir(binDir)

	if len(binDirFilesList) == 0 || errors.Is(err, fs.ErrNotExist) {
		return errNoBinariesInstalled
	} else if err != nil {
		return fmt.Errorf("error reading directory %q: %w", binDir, err)
	}

	programs := [...]Program{
		ProgramTt,
		ProgramCe,
		ProgramDev,
		ProgramEe,
		ProgramTcm,
	}

	_, _ = fmt.Fprintln(os.Stdout, "List of installed binaries:")

	for _, program := range programs {
		binaryVersions, err := ParseBinaries(binDirFilesList, program, binDir)
		if err != nil {
			return err
		}

		if len(binaryVersions) > 0 {
			sort.Stable(sort.Reverse(version.VersionSlice(binaryVersions)))
			log.Infof(program.String() + ":")

			for _, binVersion := range binaryVersions {
				printInstalledVersion(binVersion.Str)
			}
		}
	}

	return nil
}
