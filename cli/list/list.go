package list

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/apex/log"
	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// ListInstances shows enabled applications.
func ListInstances(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) error {
	instanceDir := cliOpts.App.InstancesEnabled
	if _, err := os.Stat(instanceDir); os.IsNotExist(err) {
		return fmt.Errorf("instances enabled directory doesn't exist: %s",
			instanceDir)
	}

	appList, err := util.CollectAppList(cmdCtx.Cli.ConfigDir,
		instanceDir, false)
	if err != nil {
		return fmt.Errorf("can't collect an application list: %s", err)
	}

	if len(appList) == 0 {
		log.Info("there are no enabled applications")
	}

	fmt.Println("List of enabled applications:")
	fmt.Printf("instances enabled directory: %s\n", instanceDir)

	for _, app := range appList {
		appLocation := strings.TrimPrefix(app.Location, instanceDir+string(os.PathSeparator))
		if !strings.HasSuffix(appLocation, ".lua") {
			appLocation = appLocation + string(os.PathSeparator)
		}
		log.Infof("%s (%s)", color.GreenString(strings.TrimSuffix(app.Name, ".lua")),
			appLocation)
		instances, _ := running.CollectInstances(app.Name, instanceDir)
		for _, inst := range instances {
			fullInstanceName := running.GetAppInstanceName(inst)
			if fullInstanceName != app.Name {
				fmt.Printf("	%s (%s)\n",
					color.YellowString(strings.TrimPrefix(fullInstanceName, app.Name+":")),
					strings.TrimPrefix(inst.AppPath, app.Location+string(os.PathSeparator)))
			}
		}
	}

	return nil
}

// printBinaries outputs installed versions of the program.
func printBinaries(program string, binList []string) {
	if len(binList) > 0 {
		log.Infof(program + ":")
		for _, bin := range binList {
			if strings.HasSuffix(bin, "[active]") {
				fmt.Printf("	%s\n", util.Bold(color.GreenString(bin)))
			} else {
				fmt.Printf("	%s\n", color.YellowString(bin))
			}
		}
	}
}

// sortBinaryVersions sorts versions of binary.
func sortBinaryVersions(binList []string) ([]string, error) {
	var versions []version.Version
	var sortedVersions []string
	var err error

	// Convert string to version (struct).
	for _, ver := range binList {
		if strings.HasPrefix(ver, "master") {
			sortedVersions = append(sortedVersions, ver)
		}
		var versionInt [3]int
		var version version.Version
		verString := strings.Split(ver, ".")
		if len(verString) != 3 {
			continue
		}
		verPatch := strings.Split(verString[2], "")
		verString[2] = verPatch[0]
		for i, ch := range verString {
			versionInt[i], err = strconv.Atoi(ch)
			if err != nil {
				return nil, err
			}
		}
		version.Major = uint64(versionInt[0])
		version.Minor = uint64(versionInt[1])
		version.Patch = uint64(versionInt[2])
		version.Str = ver

		versions = append(versions, version)
	}

	// Sort versions.
	version.SortVersions(versions)
	for i, j := 0, len(versions)-1; i < j; i, j = i+1, j-1 {
		versions[i], versions[j] = versions[j], versions[i]
	}

	for _, version := range versions {
		sortedVersions = append(sortedVersions, version.Str)
	}
	return sortedVersions, err
}

// parseBinaries seeks through fileList returning array of found versions of program.
func parseBinaries(fileList []fs.DirEntry, programName string,
	binDir string) ([]string, error) {
	var err error
	var binaryVersion []string
	binActive := ""

	// Files w/o version in name are symlinks to the active binaries.
	if util.ContainsFile(fileList, programName) {
		binActive, err = util.ResolveSymlink(filepath.Join(binDir, programName))
		if err != nil {
			return nil, err
		}
		binActive = strings.TrimPrefix(filepath.Base(binActive), programName+"_")
	}

	for _, f := range fileList {
		if f.IsDir() {
			continue
		}
		if strings.HasPrefix(f.Name(), programName) && f.Name() != programName {
			binVersion := strings.TrimPrefix(f.Name(), programName+"_")
			binVersion = strings.TrimPrefix(binVersion, "v")
			if binVersion == binActive {
				binaryVersion = append(binaryVersion, binVersion+" [active]")
			} else {
				binaryVersion = append(binaryVersion, binVersion)
			}
		}
	}

	return binaryVersion, err
}

// ListBinaries outputs installed versions of programs from bin_dir.
func ListBinaries(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) error {
	var err error

	binDir := cliOpts.App.BinDir
	binDirFilesList, err := os.ReadDir(cliOpts.App.BinDir)
	if err != nil {
		return fmt.Errorf("there was some problem reading bin_dir: %s", err)
	}

	if len(binDirFilesList) == 0 {
		return fmt.Errorf("there are no installed binaries")
	}

	programs := [...]string{"tt", "tarantool"}
	fmt.Println("List of installed binaries:")
	for _, prog := range programs {
		binaryVersion, err := parseBinaries(binDirFilesList, prog, binDir)
		if err != nil {
			return err
		}

		if len(binaryVersion) > 0 {
			// Sort version array.
			sorted, err := sortBinaryVersions(binaryVersion)
			if err != nil {
				return err
			}
			// Print the output.
			printBinaries(prog, sorted)
		}

	}

	return err
}
