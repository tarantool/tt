package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/build"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v2"
)

const (
	dirPermissions  = 0750
	filePermissions = 0666

	defaultVersion     = "0.1.0"
	defaultLongVersion = "0.1.0.0"

	varPath              = "var"
	logPath              = "log"
	runPath              = "run"
	dataPath             = "lib"
	envPath              = "env"
	binPath              = "bin"
	modulesPath          = "modules"
	instancesEnabledPath = "instances_enabled"
)

var (
	varDataPath = filepath.Join(varPath, dataPath)
	varLogPath  = filepath.Join(varPath, logPath)
	varRunPath  = filepath.Join(varPath, runPath)

	envBinPath     = filepath.Join(envPath, binPath)
	envModulesPath = filepath.Join(envPath, modulesPath)

	packageVarPath     = ""
	packageVarRunPath  = ""
	packageVarLogPath  = ""
	packageVarDataPath = ""

	packageEnvPath        = ""
	packageEnvBinPath     = ""
	packageEnvModulesPath = ""

	packageInstancesEnabledPath = ""
)

var defaultPaths = []string{
	varPath,
	logPath,
	runPath,
	dataPath,
	envPath,
	envBinPath,
	envModulesPath,
}

// prepareBundle prepares a temporary directory for packing.
// Returns a path to the prepared directory or error if it failed.
func prepareBundle(cmdCtx *cmdcontext.CmdCtx, packCtx PackCtx,
	cliOpts *config.CliOpts, buildRocks bool) (string, error) {
	var err error

	// Create temporary directory step.
	basePath, err := os.MkdirTemp("", "tt_pack")
	if err != nil {
		return "", err
	}

	defer func() {
		if err != nil {
			err := os.RemoveAll(basePath)
			if err != nil {
				log.Warnf("Failed to remove a directory %s: %s", basePath, err)
			}
		}
	}()

	prepareDefaultPackagePaths(basePath)

	err = createPackageStructure(basePath)
	if err != nil {
		return "", err
	}

	// Copy binaries step.
	if cliOpts.App.BinDir != "" &&
		((!packCtx.TarantoolIsSystem && !packCtx.WithoutBinaries) ||
			packCtx.WithBinaries) {
		err = copyBinaries(cmdCtx, packageEnvBinPath)
		if err != nil {
			return "", err
		}
	}

	// Copy modules step.
	if cliOpts.Modules != nil && cliOpts.Modules.Directory != "" {
		err = copy.Copy(cliOpts.Modules.Directory, packageEnvModulesPath)
		if err != nil {
			log.Warnf("Failed to copy modules from %s: %s", cliOpts.Modules.Directory, err)
		}
	}

	// Collect app list step.
	appList := []util.AppListEntry{}
	if packCtx.AppList == nil {
		appList, err = util.CollectAppList(cmdCtx.Cli.ConfigDir, cliOpts.App.InstancesEnabled,
			true)
		if err != nil {
			return "", err
		}
	} else {
		for _, appName := range packCtx.AppList {
			if util.IsApp(filepath.Join(cliOpts.App.InstancesEnabled, appName)) {
				appList = append(appList, util.AppListEntry{
					Name:     appName,
					Location: filepath.Join(cliOpts.App.InstancesEnabled, appName),
				})
			} else {
				log.Warnf("Skip packing of '%s': specified name is not an application.", appName)
			}
		}
	}

	if len(appList) == 0 {
		err = fmt.Errorf("there are no apps found in instance_enabled directory")
		return "", err
	}

	{
		appsToPack := ""
		for _, appInfo := range appList {
			appsToPack += appInfo.Name + " "
		}
		log.Infof("Apps to pack: %s", appsToPack)
	}

	// Copy all apps to a temp directory step.
	for _, appInfo := range appList {
		err = copyAppSrc(appInfo.Location, basePath)
		if err != nil {
			return "", err
		}

		if packCtx.Archive.All {
			err = copyArtifacts(cliOpts, appInfo.Name)
			if err != nil {
				return "", err
			}
		}

		err = createAppSymlink(appInfo.Location, appInfo.Name)
		if err != nil {
			return "", err
		}
	}

	if buildRocks {
		err = buildAllRocks(cmdCtx, cliOpts, basePath)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}

	err = createEnv(cliOpts, basePath)
	if err != nil {
		return "", err
	}
	return basePath, nil
}

// createPackageStructure initializes a standard package structure in passed directory.
func createPackageStructure(destPath string) error {
	basePaths := []string{
		destPath,
		packageVarRunPath,
		packageVarLogPath,
		packageVarDataPath,
		packageEnvBinPath,
		packageEnvModulesPath,
		packageInstancesEnabledPath,
	}

	for _, path := range basePaths {
		err := os.MkdirAll(path, dirPermissions)
		if err != nil {
			return err
		}
	}
	return nil
}

// copyAppSrc copies a source file or directory to the directory, that will be packed.
func copyAppSrc(appPath string, packagePath string) error {
	appPath, err := filepath.EvalSymlinks(appPath)
	if err != nil {
		return err
	}

	if _, err = os.Stat(appPath); err != nil {
		return err
	}

	// Copying application.
	err = copy.Copy(appPath, filepath.Join(packagePath, filepath.Base(appPath)), copy.Options{
		Skip: func(src string) (bool, error) {
			fileInfo, err := os.Stat(src)
			if err != nil {
				return false, fmt.Errorf("failed to check the source: %s", src)
			}
			perm := fileInfo.Mode()
			if perm&os.ModeSocket != 0 {
				return true, nil
			}

			if strings.HasPrefix(src, ".git") {
				return true, nil
			}
			return false, nil
		},
	})
	if err != nil {
		return err
	}
	return nil
}

// copyArtifacts copies all artifacts from the current bundle configuration
// to the passed package structure from the passed path.
func copyArtifacts(opts *config.CliOpts, appName string) error {
	log.Infof("Copying all artifacts")

	ext := filepath.Ext(appName)
	if ext == ".lua" {
		appName = appName[:len(appName)-len(ext)]
	}
	err := copy.Copy(filepath.Join(opts.App.WalDir, appName),
		filepath.Join(packageVarDataPath, appName))
	if err != nil {
		return err
	}
	if opts.App.VinylDir != opts.App.WalDir {
		err := copy.Copy(filepath.Join(opts.App.VinylDir, appName),
			filepath.Join(packageVarDataPath, appName))
		if err != nil {
			return err
		}
	}
	if opts.App.MemtxDir != opts.App.WalDir && opts.App.MemtxDir != opts.App.VinylDir {
		err := copy.Copy(filepath.Join(opts.App.MemtxDir, appName),
			filepath.Join(packageVarDataPath, appName))
		if err != nil {
			return err
		}
	}
	err = copy.Copy(filepath.Join(opts.App.LogDir, appName),
		filepath.Join(packageVarLogPath, appName))
	if err != nil {
		return err
	}
	return nil
}

// TODO replace by tt enable
// createAppSymlink creates a relative link for an application that must be packed.
func createAppSymlink(appPath string, appName string) error {
	var err error
	appPath, err = filepath.EvalSymlinks(appPath)
	if err != nil {
		return err
	}

	err = os.Symlink(filepath.Join("..", filepath.Base(appPath)),
		filepath.Join(packageInstancesEnabledPath, appName))
	if err != nil {
		return err
	}
	return nil
}

// createEnv generates a tt.yaml file.
func createEnv(opts *config.CliOpts, destPath string) error {
	log.Infof("Generating new %s for the new package", configure.ConfigName)

	appOpts := config.AppOpts{
		InstancesEnabled: instancesEnabledPath,
		BinDir:           filepath.Join(envPath, binPath),
		RunDir:           filepath.Join(varPath, runPath),
		WalDir:           filepath.Join(varPath, dataPath),
		VinylDir:         filepath.Join(varPath, dataPath),
		MemtxDir:         filepath.Join(varPath, dataPath),
		LogDir:           filepath.Join(varPath, logPath),
		LogMaxSize:       opts.App.LogMaxSize,
		LogMaxAge:        opts.App.LogMaxAge,
		LogMaxBackups:    opts.App.LogMaxBackups,
		Restartable:      opts.App.Restartable,
	}
	moduleOpts := config.ModulesOpts{
		Directory: filepath.Join(envPath, modulesPath),
	}
	cliOptsNew := config.CliOpts{
		App:     &appOpts,
		Modules: &moduleOpts,
	}
	cfg := config.Config{
		CliConfig: &cliOptsNew,
	}

	file, err := os.Create(filepath.Join(destPath, configure.ConfigName))
	if err != nil {
		return err
	}
	defer func() {
		err := file.Close()
		if err != nil {
			log.Warnf("Failed to close a file %s: %s", file.Name(), err)
		}
	}()

	err = yaml.NewEncoder(file).Encode(&cfg)
	if err != nil {
		return err
	}
	return nil
}

// findRocks tries to find a rockspec file, starting from the passed root directory.
func findRocks(root string) (string, error) {
	pattern := "*.rockspec"
	res := ""
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if matched, err := filepath.Match(pattern, filepath.Base(path)); err != nil {
			return err
		} else if matched {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			res = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if res == "" {
		return "", fmt.Errorf("rockspec not found")
	}
	return res, nil
}

// buildAllRocks finds a rockspec file of the application and builds it.
func buildAllRocks(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, destPath string) error {
	entries, err := os.ReadDir(destPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			rockspecPath, err := findRocks(filepath.Join(destPath, entry.Name()))
			if err != nil && err.Error() == "rockspec not found" {
				continue
			}
			if err != nil {
				return err
			}
			buildCtx := build.BuildCtx{BuildDir: filepath.Dir(rockspecPath)}
			err = build.Run(cmdCtx, cliOpts, &buildCtx)
			if err != nil {
				return err
			}
			log.Infof("%s rocks are built successfully", entry.Name())
		}
	}

	return err
}

// prepareDefaultPackagePaths defines all default paths for the directory, where
// the package will be built.
func prepareDefaultPackagePaths(packagePath string) {
	packageVarPath = filepath.Join(packagePath, varPath)
	packageVarRunPath = filepath.Join(packageVarPath, runPath)
	packageVarLogPath = filepath.Join(packageVarPath, logPath)
	packageVarDataPath = filepath.Join(packageVarPath, dataPath)

	packageEnvPath = filepath.Join(packagePath, envPath)
	packageEnvBinPath = filepath.Join(packageEnvPath, binPath)
	packageEnvModulesPath = filepath.Join(packageEnvPath, modulesPath)

	packageInstancesEnabledPath = filepath.Join(packagePath, instancesEnabledPath)
}

// getVersion returns a version of the package.
// The version depends on passed pack context.
func getVersion(packCtx *PackCtx, opts *config.CliOpts, defaultVersion string) string {
	packageVersion := defaultVersion
	if packCtx.Version == "" {
		// Get version from git only if we are packing an application from the current directory.
		if opts.App.InstancesEnabled == "." {
			version, err := util.CheckVersionFromGit(opts.App.InstancesEnabled)
			if err == nil || version != "" {
				packageVersion = version
			}
		}
	} else {
		packageVersion = packCtx.Version
	}
	return packageVersion
}

// copyBinaries copies tarantool and tt binaries from the current
// tt environment to the passed destination path.
func copyBinaries(cmdCtx *cmdcontext.CmdCtx, destPath string) error {
	ttBin, err := os.Executable()
	if err != nil {
		return err
	}
	realPath, err := filepath.EvalSymlinks(ttBin)
	if err != nil {
		log.Warnf("Failed to access %s: %s", ttBin, err)
	}
	if realPath != "" {
		ttBin = realPath
	}

	err = copy.Copy(ttBin, filepath.Join(destPath, filepath.Base(ttBin)))
	if err != nil {
		return err
	}

	tntBin, err := filepath.EvalSymlinks(cmdCtx.Cli.TarantoolExecutable)
	if err != nil {
		log.Warnf("Failed to access %s: %s", tntBin, err)
	}
	if tntBin == "" {
		tntBin = cmdCtx.Cli.TarantoolExecutable
	}

	err = copy.Copy(tntBin, filepath.Join(destPath, filepath.Base(tntBin)))
	if err != nil {
		return err
	}

	return nil
}

// getPackageName returns the result name of the package.
func getPackageName(packCtx *PackCtx, opts *config.CliOpts, suffix string,
	addVersion bool) (string, error) {
	var packageName string

	if packCtx.FileName != "" {
		return packCtx.FileName, nil
	} else if packCtx.Name != "" {
		packageName = packCtx.Name
	} else {
		absPath, err := filepath.Abs(".")
		if err != nil {
			return "", err
		}
		packageName = filepath.Base(absPath)
	}

	if addVersion {
		var separator string
		switch packCtx.Type {
		case Tgz, Rpm:
			separator = "-"
		case Deb:
			separator = "_"
		}
		versionSuffix := getVersion(packCtx, opts, defaultLongVersion)
		packageName += separator + versionSuffix
	}

	packageName += suffix
	return packageName, nil
}
