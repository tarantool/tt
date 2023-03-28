package list

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apex/log"
	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/search"
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
func printVersion(versionString string) {
	if strings.HasSuffix(versionString, "[active]") {
		fmt.Printf("	%s\n", util.Bold(color.GreenString(versionString)))
	} else {
		fmt.Printf("	%s\n", color.YellowString(versionString))
	}
}

// parseBinaries seeks through fileList returning array of found versions of program.
func parseBinaries(fileList []fs.DirEntry, programName string,
	binDir string) ([]version.Version, error) {
	var binaryVersions []version.Version
	binActive, err := util.ResolveSymlink(filepath.Join(binDir, programName))
	if err != nil && !os.IsNotExist(err) {
		return binaryVersions, err
	}
	binActive = filepath.Base(binActive)

	versionPrefix := programName + version.FsSeparator
	for _, f := range fileList {
		if strings.HasPrefix(f.Name(), versionPrefix) {
			versionStr := strings.TrimPrefix(strings.TrimPrefix(f.Name(), versionPrefix), "v")
			if versionStr == "master" {
				binaryVersions = append(binaryVersions, version.Version{
					Major: math.MaxUint, // Small hack to make master the newest version.
					Str:   "master",
				})
			} else {
				ver, err := version.Parse(versionStr)
				if err != nil {
					return binaryVersions, err
				}
				if binActive == f.Name() {
					ver.Str += " [active]"
				}
				binaryVersions = append(binaryVersions, ver)
			}
		}
	}

	return binaryVersions, nil
}

// ListBinaries outputs installed versions of programs from bin_dir.
func ListBinaries(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) (err error) {
	binDir := cliOpts.App.BinDir
	binDirFilesList, err := os.ReadDir(binDir)
	if err != nil {
		return fmt.Errorf("error reading directory %q: %s", binDir, err)
	}

	if len(binDirFilesList) == 0 {
		return fmt.Errorf("there are no installed binaries")
	}

	programs := [...]string{search.ProgramTt, search.ProgramCe}
	fmt.Println("List of installed binaries:")
	for _, programName := range programs {
		binaryVersions, err := parseBinaries(binDirFilesList, programName, binDir)
		if err != nil {
			return err
		}

		if len(binaryVersions) > 0 {
			sort.Stable(sort.Reverse(version.VersionSlice(binaryVersions)))
			log.Infof(programName + ":")
			for _, binVersion := range binaryVersions {
				printVersion(binVersion.Str)
			}
		}

	}

	return err
}
