package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/build"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v2"
)

const (
	dirPermissions  = 0750
	filePermissions = 0666

	defaultVersion = "0.1.0.0"

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

var defaultExcludeListExpressions = []string{
	"\\w+\\/.+",   // stops to watching apps inside the directories
	"^\\.\\w+",    // stops from indexing files starting with . like .rocks
	"^\\.$",       // excludes a hard link to the current directory.
	"\\w+.yml",    // excludes all yml files
	"\\w+.yaml",   // excludes all yaml files
	"\\w+.tar.gz", // excludes all tarballs
}

// prepareBundle prepares a temporary directory for packing.
// Returns a path to the prepared directory or error if it failed.
func prepareBundle(cmdCtx *cmdcontext.CmdCtx) (string, error) {
	var err error
	packCtx := &cmdCtx.Pack

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
	if packCtx.App.BinDir != "" &&
		((!packCtx.TarantoolIsSystem && !packCtx.WithoutBinaries) ||
			packCtx.WithBinaries) {
		err = copyBinaries(packCtx.App.BinDir, packageEnvBinPath)
		//err = copy.Copy(packCtx.App.BinDir, packageEnvBinPath)
		if err != nil {
			return "", err
		}
	}

	// Copy modules step.
	if packCtx.ModulesDirectory != "" {
		err = copy.Copy(packCtx.ModulesDirectory, packageEnvModulesPath)
		if err != nil {
			return "", err
		}
	}

	// Initialize a default list of strings for preparing a black list.
	ExcludeListExpressions := prepareDefaultExcludeListExpressions()
	excludeList, err := prepareExcludeList(ExcludeListExpressions)
	if err != nil {
		return "", fmt.Errorf("failed to compile regular expressions: %s", err)
	}

	// Collect app list step.
	appList := []string{}
	if packCtx.AppList == nil {
		if packCtx.App.InstancesEnabled != "." {
			appList, err = collectAppList(packCtx.App.InstancesEnabled, excludeList)
			if err != nil {
				return "", err
			}
		} else {
			// If running tt pack with instances_enabled: .
			// it supposed to pack a current directory as an application.
			appPath, err := os.Getwd()
			if err != nil {
				return "", err
			}
			if util.IsApp(appPath, excludeList) {
				appList = []string{filepath.Base(appPath)}
			}

			newInstancesEnabledPath, err := filepath.Abs("..")
			if err != nil {
				return "", err
			}
			packCtx.App.InstancesEnabled = newInstancesEnabledPath
		}
	} else {
		for _, appName := range packCtx.AppList {
			if util.IsApp(filepath.Join(packCtx.App.InstancesEnabled, appName), excludeList) {
				appList = append(appList, appName)
			} else {
				log.Warnf("Skip packing of '%s': specified name is not an application.", appName)
			}
		}
	}

	if len(appList) == 0 {
		return "", fmt.Errorf("The is no apps found in instance_enabled directory")
	}

	log.Infof("Apps to pack: %v", strings.Join(appList, " "))

	// Copy all apps to a temp directory step.
	for _, appName := range appList {
		err = copyAppSrc(packCtx, appName, basePath)
		if err != nil {
			return "", err
		}

		if packCtx.Archive.All {
			err = copyArtifacts(packCtx, appName)
			if err != nil {
				return "", err
			}
		}

		err = createAppSymlink(packCtx, appName)
		if err != nil {
			return "", err
		}
	}

	err = buildAllRocks(cmdCtx, basePath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	err = createEnv(packCtx, basePath)
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
func copyAppSrc(packCtx *cmdcontext.PackCtx, appName, packagePath string) error {
	pathToCopy, err := resolveAppName(packCtx.App.InstancesEnabled, appName)
	if err != nil {
		return err
	}
	if _, err = os.Stat(pathToCopy); err != nil {
		return err
	}

	// Copying application.
	err = copy.Copy(pathToCopy, filepath.Join(packagePath, filepath.Base(pathToCopy)), copy.Options{
		Skip: func(src string) (bool, error) {
			fileInfo, err := os.Stat(src)
			if err != nil {
				return false, fmt.Errorf("Failed to check the source: %s", src)
			}
			perm := fileInfo.Mode()
			if perm&os.ModeSocket != 0 {
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
func copyArtifacts(packCtx *cmdcontext.PackCtx, appName string) error {
	log.Infof("Copying all artifacts")

	err := copy.Copy(filepath.Join(packCtx.App.DataDir, appName),
		filepath.Join(packageVarDataPath, appName))
	if err != nil {
		return err
	}
	err = copy.Copy(filepath.Join(packCtx.App.LogDir, appName),
		filepath.Join(packageVarLogPath, appName))
	if err != nil {
		return err
	}
	return nil
}

// TODO replace by tt enable
// createAppSymlink creates a relative link for an application that must be packed.
func createAppSymlink(packCtx *cmdcontext.PackCtx, appName string) error {
	appPath, err := resolveAppName(packCtx.App.InstancesEnabled, appName)
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

// createEnv generates a tarantool.yaml file.
func createEnv(packCtx *cmdcontext.PackCtx, destPath string) error {
	log.Infof("Generating new tarantool.yaml for the new package")

	appOpts := config.AppOpts{
		InstancesEnabled:   instancesEnabledPath,
		InstancesAvailable: ".",
		BinDir:             filepath.Join(envPath, binPath),
		RunDir:             filepath.Join(varPath, runPath),
		DataDir:            filepath.Join(varPath, dataPath),
		LogDir:             filepath.Join(varPath, logPath),
		LogMaxSize:         packCtx.App.LogMaxSize,
		LogMaxAge:          packCtx.App.LogMaxAge,
		LogMaxBackups:      packCtx.App.LogMaxBackups,
		Restartable:        packCtx.App.Restartable,
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

	file, err := os.Create(filepath.Join(destPath, "tarantool.yaml"))
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

// resolveAppName accepts a normalized name of application and its location,
// detects if it is a file/directory/symlink and returns a path to it.
func resolveAppName(baseDir, appName string) (string, error) {
	appPath := filepath.Join(baseDir, appName)
	// Detecting if the application is a file or a directory.
	_, err := os.Stat(appPath)
	if os.IsNotExist(err) {
		appPath = appPath + ".lua"
		_, err = os.Stat(appPath)
		if err != nil {
			return "", err
		}
	}

	pathToCopy := appPath
	realPath, err := filepath.EvalSymlinks(appPath)
	if err != nil {
		return "", err
	}
	if realPath != "" {
		pathToCopy = realPath
	}

	return pathToCopy, nil
}

// prepareDefaultExcludeListExpressions returns a default list of expressions
// for compiling to regular expressions.
func prepareDefaultExcludeListExpressions() []string {
	// Complete the list of black list expressions with default paths.
	return append(defaultExcludeListExpressions, defaultPaths...)
}

// prepareExcludeList accepts a slice of expressions to be compiled as regexp.
func prepareExcludeList(expressions []string) ([]*regexp.Regexp, error) {
	excludeList := []*regexp.Regexp{}

	for _, expression := range expressions {
		regex, err := regexp.Compile(expression)
		if err != nil {
			return nil, err
		}
		excludeList = append(excludeList, regex)
	}
	return excludeList, nil
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

// collectAppList collects all the supposed applications from the passed directory,
// considering the passed slice of exclude regexp items.
func collectAppList(baseDir string, excludeList []*regexp.Regexp) ([]string, error) {
	dirEnrties, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}

	apps := make([]string, 0)
	for _, entry := range dirEnrties {
		dirItem := filepath.Join(baseDir, entry.Name())
		if util.IsApp(dirItem, excludeList) {
			app := appNameFromEntry(entry)
			if app != "" {
				apps = append(apps, app)
			}
		} else {
			log.Warnf("The application %s can't be packed: failed to access the source",
				entry.Name())
		}
	}

	if err != nil {
		return nil, err
	}
	return apps, nil
}

// appNameFromEntry returns a normalized application name.
// If the application is a lua file, the name of file will be returned
// without its extension.
func appNameFromEntry(entry os.DirEntry) string {
	if filepath.Ext(entry.Name()) == ".lua" {
		return entry.Name()[:len(entry.Name())-len(".lua")]
	}
	if entry.IsDir() || entry.Type() == os.ModeSymlink {
		return entry.Name()
	}
	return ""
}

// buildAllRocks finds a rockspec file of the application and builds it.
func buildAllRocks(cmdCtx *cmdcontext.CmdCtx, destPath string) error {
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
			cmdCtx.Build.BuildDir = filepath.Dir(rockspecPath)
			err = build.Run(cmdCtx)
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
func getVersion(packCtx *cmdcontext.PackCtx) string {
	var packageVersion string
	var err error
	if packCtx.Version == "" {
		packageVersion, err =
			util.CheckVersionFromGit(packCtx.App.InstancesAvailable)
		if err != nil {
			packageVersion = defaultVersion
		}
	} else {
		packageVersion = packCtx.Version
	}
	return packageVersion
}

// copyBinaries copies tarantool and tt binaries from passed source path
// to the passed destination path.
func copyBinaries(srcPath, destPath string) error {
	entries, err := os.ReadDir(srcPath)
	if err != nil {
		return err
	}
	for _, binary := range entries {
		if binary.IsDir() {
			log.Warnf("Cannot copy %s from binary directory.", binary.Name())
			continue
		}

		pathToCopy := filepath.Join(srcPath, binary.Name())
		realPath, err := filepath.EvalSymlinks(filepath.Join(srcPath, binary.Name()))
		if err != nil {
			log.Warnf("Failed to access %s: %s", binary, err)
		}
		if realPath != "" {
			pathToCopy = realPath
		}
		err = copy.Copy(pathToCopy, filepath.Join(destPath, binary.Name()))
		if err != nil {
			log.Warnf("Failed to copy %s: %s", binary, err)
		}
	}

	return nil
}
