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
	// ErrNoBinariesInstalled reports a scope without installed binaries.
	ErrNoBinariesInstalled = errors.New(
		"there are no binaries installed in the selected scope",
	)
)

// ListCtx contains the options for listing installed binaries.
type ListCtx struct {
	binary.BinaryContext
}

// printInstalledVersion outputs installed versions of the program.
func printInstalledVersion(versionString string) {
	if strings.HasSuffix(versionString, "[active]") {
		_, _ = fmt.Fprintf(os.Stdout, "\t%s\n", binary.Bold(color.GreenString(versionString)))
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "\t%s\n", color.YellowString(versionString))
	}
}

// displayVersion parses ordering information without changing installation identity.
func displayVersion(raw string) (version.Version, error) {
	name := strings.TrimPrefix(raw, "v")
	isCommit, _ := binary.IsValidCommitHash(name)

	var parsed version.Version

	switch {
	case name == "master":
		parsed.Major = math.MaxUint // Keep master before release versions.
	case isCommit:
		// Commit hashes keep their stable directory order after release versions.
	default:
		var err error

		parsed, err = version.Parse(name)
		if err != nil {
			return parsed, err
		}
	}

	parsed.Str = raw

	return parsed, nil
}

// regularTarantoolVersion preserves support for an unversioned executable in this scope.
func regularTarantoolVersion(root string) (version.Version, bool, error) {
	var parsed version.Version

	programPath := filepath.Join(root, binary.BinDirName, binary.ProgramCe.Exec())
	info, err := os.Lstat(programPath)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return parsed, false, nil
	case err != nil:
		return parsed, false, fmt.Errorf("failed to access %q: %w", programPath, err)
	case !info.Mode().IsRegular():
		return parsed, false, nil
	}

	tntCli := cmdcontext.TarantoolCli{Executable: programPath}

	parsed, err = tntCli.GetVersion()
	if err != nil {
		return parsed, false, err
	}

	parsed.Str += " [active]"

	return parsed, true, nil
}

// ParseBinaries reads installed executables from the selected product's version entries.
// Str preserves the exact installation suffix, followed by an optional active marker.
func ParseBinaries(fileList []fs.DirEntry, program binary.Program,
	root string,
) ([]version.Version, error) {
	active, err := binary.ResolveSymlink(filepath.Join(root, binary.BinDirName, program.Exec()))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var versions []version.Version

	for _, entry := range fileList {
		selected, found := binary.InstallationVersion(program, entry.Name())
		if !entry.IsDir() || !found {
			continue
		}

		var isActive bool

		switch program {
		case binary.ProgramSDK:
			sdk, err := binary.ReadSDK(root, selected)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			if err != nil {
				return nil, err
			}

			err = binary.ValidateSDKManifest(sdk, selected)
			if err != nil {
				return nil, err
			}

			err = binary.ValidateSDKFiles(root, sdk)
			if err == nil {
				isActive = binary.SDKActive(root, sdk)
			}
		default:
			binPath := filepath.Join(root, binary.VersionsDirName, program.String(),
				entry.Name(), program.Exec())
			info, err := os.Stat(binPath)

			switch {
			case errors.Is(err, os.ErrNotExist):
				continue
			case err != nil:
				return nil, fmt.Errorf("failed to access %q: %w", binPath, err)
			case !info.Mode().IsRegular():
				continue
			}

			resolved, err := binary.ResolveSymlink(binPath)
			if err != nil {
				return nil, err
			}

			isActive = active == resolved
		}

		parsed, err := displayVersion(selected)
		if err != nil {
			return nil, err
		}

		if isActive {
			parsed.Str += " [active]"
		}

		versions = append(versions, parsed)
	}

	return versions, nil
}

// installedVersions prepares the selected product's versions for sorted display.
func installedVersions(root string, program binary.Program) ([]version.Version, error) {
	dir := filepath.Join(root, binary.VersionsDirName, program.String())
	entries, err := os.ReadDir(dir)

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("error reading directory %q: %w", dir, err)
	}

	versions, err := ParseBinaries(entries, program, root)
	if err != nil {
		return nil, err
	}

	if program == binary.ProgramCe {
		active, found, err := regularTarantoolVersion(root)
		if err != nil {
			return nil, err
		}

		if found {
			versions = append(versions, active)
		}
	}

	sort.Stable(sort.Reverse(version.VersionSlice(versions)))

	return versions, nil
}

// ListBinaries outputs installed versions from exactly the selected scope.
func ListBinaries(listCtx ListCtx) error {
	root, err := listCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	buildPath, err := binary.TarantoolBuildPath(filepath.Join(root, binary.BinDirName))
	if err != nil {
		return err
	}

	programs := [...]binary.Program{
		binary.ProgramTt,
		binary.ProgramTtEe,
		binary.ProgramCe,
		binary.ProgramEe,
		binary.ProgramTcm,
		binary.ProgramSDK,
	}
	versions := make(map[binary.Program][]version.Version, len(programs))
	hasBinaries := buildPath != ""

	for _, program := range programs {
		versions[program], err = installedVersions(root, program)
		if err != nil {
			return err
		}

		hasBinaries = hasBinaries || len(versions[program]) > 0
	}

	if !hasBinaries {
		return ErrNoBinariesInstalled
	}

	_, _ = fmt.Fprintln(os.Stdout, "List of installed binaries:")

	for _, program := range programs {
		isLocalBuild := program == binary.ProgramCe && buildPath != ""
		if len(versions[program]) == 0 && !isLocalBuild {
			continue
		}

		log.Infof(program.String() + ":")

		for _, binVersion := range versions[program] {
			printInstalledVersion(strings.TrimPrefix(binVersion.Str, "v"))
		}

		if isLocalBuild {
			printInstalledVersion(program.String() + " -> " + buildPath + " [active]")
		}
	}

	return nil
}
