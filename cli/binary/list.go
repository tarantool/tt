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

	"github.com/tarantool/tt/cli/install"

	"github.com/apex/log"
	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// printBinaries outputs installed versions of the program.
func printVersion(versionString string) {
	if strings.HasSuffix(versionString, "[active]") {
		fmt.Printf("	%s\n", util.Bold(color.GreenString(versionString)))
	} else {
		fmt.Printf("	%s\n", color.YellowString(versionString))
	}
}

// ParseBinaries seeks through fileList returning array of found versions of program.
func ParseBinaries(fileList []fs.DirEntry, program search.Program,
	binDir string) ([]version.Version, error) {
	var binaryVersions []version.Version

	symlinkName := program.Exec()

	binActive := ""
	programPath := filepath.Join(binDir, symlinkName)
	if fileInfo, err := os.Lstat(programPath); err == nil {
		if program == search.ProgramDev &&
			fileInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
			binActive, isTarantoolBinary, err := install.IsTarantoolDev(programPath, binDir)
			if err != nil {
				return binaryVersions, err
			}
			if isTarantoolBinary {
				binaryVersions = append(binaryVersions,
					version.Version{Str: program.String() + " -> " + binActive + " [active]"})
			}
			return binaryVersions, nil
		} else if program == search.ProgramCe && fileInfo.Mode()&os.ModeSymlink == 0 {
			tntCli := cmdcontext.TarantoolCli{Executable: programPath}
			binaryVersion, err := tntCli.GetVersion()
			if err != nil {
				return binaryVersions, err
			}
			binaryVersion.Str += " [active]"
			binaryVersions = append(binaryVersions, binaryVersion)
		} else {
			binActive, err = util.ResolveSymlink(programPath)
			if err != nil && !os.IsNotExist(err) {
				return binaryVersions, err
			}
			binActive = filepath.Base(binActive)
		}
	}

	versionPrefix := program.String() + version.FsSeparator
	var err error
	for _, f := range fileList {
		if strings.HasPrefix(f.Name(), versionPrefix) {
			versionStr := strings.TrimPrefix(strings.TrimPrefix(f.Name(), versionPrefix), "v")
			var ver version.Version
			isRightFormat, _ := util.IsValidCommitHash(versionStr)
			if versionStr == "master" {
				ver.Major = math.MaxUint // Small hack to make master the newest version.
			} else if !isRightFormat {
				ver, err = version.Parse(versionStr)
				if err != nil {
					return binaryVersions, err
				}
			}

			if binActive == f.Name() {
				ver.Str = versionStr + " [active]"
			} else {
				ver.Str = versionStr
			}
			binaryVersions = append(binaryVersions, ver)

		}
	}

	return binaryVersions, nil
}

// ListBinaries outputs installed versions of programs from bin_dir.
func ListBinaries(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) (err error) {
	binDir := cliOpts.Env.BinDir
	binDirFilesList, err := os.ReadDir(binDir)

	if len(binDirFilesList) == 0 || errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("there are no binaries installed in this environment of 'tt'")
	} else if err != nil {
		return fmt.Errorf("error reading directory %q: %s", binDir, err)
	}

	programs := [...]search.Program{
		search.ProgramTt,
		search.ProgramCe,
		search.ProgramDev,
		search.ProgramEe,
		search.ProgramTcm,
	}
	fmt.Println("List of installed binaries:")
	for _, program := range programs {
		binaryVersions, err := ParseBinaries(binDirFilesList, program, binDir)
		if err != nil {
			return err
		}

		if len(binaryVersions) > 0 {
			sort.Stable(sort.Reverse(version.VersionSlice(binaryVersions)))
			log.Infof(program.String() + ":")
			for _, binVersion := range binaryVersions {
				printVersion(binVersion.Str)
			}
		}

	}

	return err
}
