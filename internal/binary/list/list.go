package list

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
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	// ErrNoBinariesInstalled reports an environment without installed binaries.
	ErrNoBinariesInstalled = errors.New(
		"there are no binaries installed in this environment of 'tt'",
	)
)

// ListCtx contains the options for listing installed binaries.
type ListCtx struct {
	binary.BinaryContext

	BinDir string
}

// printInstalledVersion outputs installed versions of the program.
func printInstalledVersion(versionString string) {
	if strings.HasSuffix(versionString, "[active]") {
		_, _ = fmt.Fprintf(os.Stdout, "	%s\n", binary.Bold(color.GreenString(versionString)))
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "	%s\n", color.YellowString(versionString))
	}
}

// activeBinaryName resolves the active version's filename, excluding linked local builds.
func activeBinaryName(program binary.Program, programPath, binDir string) (string, error) {
	if program.IsTarantool() {
		buildPath, err := binary.TarantoolBuildPath(binDir)
		if err != nil {
			return "", err
		}

		if buildPath != "" {
			return "", nil
		}
	}

	activePath, err := binary.ResolveSymlink(programPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	return filepath.Base(activePath), nil
}

// ParseBinaries seeks through fileList returning array of found versions of program.
func ParseBinaries(fileList []fs.DirEntry, program binary.Program,
	binDir string,
) ([]version.Version, error) {
	var binaryVersions []version.Version

	symlinkName := program.Exec()

	binActive := ""
	programPath := filepath.Join(binDir, symlinkName)

	fileInfo, err := os.Lstat(programPath)
	if err == nil {
		switch {
		case program == binary.ProgramCe && fileInfo.Mode()&os.ModeSymlink == 0:
			tntCli := cmdcontext.TarantoolCli{Executable: programPath}

			binaryVersion, err := tntCli.GetVersion()
			if err != nil {
				return binaryVersions, err
			}

			binaryVersion.Str += " [active]"

			binaryVersions = append(binaryVersions, binaryVersion)
		default:
			binActive, err = activeBinaryName(program, programPath, binDir)
			if err != nil {
				return binaryVersions, err
			}
		}
	}

	versionPrefix := program.String() + version.FsSeparator

	for _, entry := range fileList {
		if !strings.HasPrefix(entry.Name(), versionPrefix) {
			continue
		}

		versionStr := strings.TrimPrefix(strings.TrimPrefix(entry.Name(), versionPrefix), "v")

		var ver version.Version

		isRightFormat, _ := binary.IsValidCommitHash(versionStr)

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
func ListBinaries(listCtx ListCtx) error {
	binDir := listCtx.BinDir
	binDirFilesList, err := os.ReadDir(binDir)

	if len(binDirFilesList) == 0 || errors.Is(err, fs.ErrNotExist) {
		return ErrNoBinariesInstalled
	} else if err != nil {
		return fmt.Errorf("error reading directory %q: %w", binDir, err)
	}

	buildPath, err := binary.TarantoolBuildPath(binDir)
	if err != nil {
		return err
	}

	programs := [...]binary.Program{
		binary.ProgramTt,
		binary.ProgramCe,
		binary.ProgramEe,
		binary.ProgramTcm,
	}

	_, _ = fmt.Fprintln(os.Stdout, "List of installed binaries:")

	for _, program := range programs {
		binaryVersions, err := ParseBinaries(binDirFilesList, program, binDir)
		if err != nil {
			return err
		}

		isLocalBuild := program == binary.ProgramCe && buildPath != ""
		if len(binaryVersions) > 0 || isLocalBuild {
			sort.Stable(sort.Reverse(version.VersionSlice(binaryVersions)))
			log.Infof(program.String() + ":")

			for _, binVersion := range binaryVersions {
				printInstalledVersion(binVersion.Str)
			}

			if isLocalBuild {
				printInstalledVersion(program.String() + " -> " + buildPath + " [active]")
			}
		}
	}

	return nil
}
