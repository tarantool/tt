package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/build"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/integrity"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	lua "github.com/yuin/gopher-lua"
	"gopkg.in/yaml.v2"
)

const (
	dirPermissions  = 0750
	filePermissions = 0666

	defaultVersion     = "0.1.0"
	defaultLongVersion = "0.1.0.0"

	versionFileName    = "VERSION"
	versionLuaFileName = "VERSION.lua"

	rocksManifestPath = ".rocks/share/tarantool/rocks/manifest"
)

var (
	versionRgxps = []*regexp.Regexp{
		regexp.MustCompile(`^(?P<Major>\d+)$`),
		regexp.MustCompile(`^(?P<Major>\d+)\.(?P<Minor>\d+)$`),
		regexp.MustCompile(`^(?P<Major>\d+)\.(?P<Minor>\d+)\.(?P<Patch>\d+)$`),
		regexp.MustCompile(`^(?P<Major>\d+)\.(?P<Minor>\d+)\.(?P<Patch>\d+)-(?P<Count>\d+)$`),
		regexp.MustCompile(`^(?P<Major>\d+)\.(?P<Minor>\d+)\.(?P<Patch>\d+)-(?P<Hash>g\w+)$`),
		regexp.MustCompile(
			`^(?P<Major>\d+)\.(?P<Minor>\d+)\.(?P<Patch>\d+)-(?P<Count>\d+)-(?P<Hash>g\w+)$`,
		),
		regexp.MustCompile(
			`^v(?P<Major>\d+)\.(?P<Minor>\d+)\.(?P<Patch>\d+)-(?P<Count>\d+)-(?P<Hash>g\w+)$`,
		),
	}
)

type RocksVersions map[string][]string

// skipDefaults filters out sockets and git dirs.
func skipDefaults(src string) (bool, error) {
	fileInfo, err := os.Stat(src)
	if err != nil {
		return false, fmt.Errorf("failed to check the source: %s", src)
	}
	perm := fileInfo.Mode()
	if perm&os.ModeSocket != 0 {
		return true, nil
	}

	if strings.HasPrefix(src, ".git") ||
		strings.Contains(src, "/.git") {
		return true, nil
	}
	return false, nil
}

// prepeareArtifactFilters prepares a slice of filters for runtime artifacts.
func prepeareArtifactFilters(cliOpts *config.CliOpts) []func(src string) bool {
	filters := make([]func(src string) bool, 0)
	if cliOpts == nil || cliOpts.App == nil {
		return filters
	}
	trim := func(src string) string {
		return strings.TrimRight(strings.TrimLeft(src, "."), "/")
	}
	for _, dataDir := range [...]string{cliOpts.App.LogDir, cliOpts.App.RunDir, cliOpts.App.WalDir,
		cliOpts.App.MemtxDir, cliOpts.App.VinylDir} {
		if dataDir != "" && !filepath.IsAbs(dataDir) {
			artifactSuffix := trim(dataDir)
			filters = append(filters, func(src string) bool {
				return strings.HasSuffix(src, artifactSuffix)
			})
		}
	}
	return filters
}

// skipArtifacts returns a filter func to filter out artifacts paths.
func skipArtifacts(cliOpts *config.CliOpts) func(src string) (bool, error) {
	artifactFilters := prepeareArtifactFilters(cliOpts)

	return func(src string) (bool, error) {
		if skip, err := skipDefaults(src); err != nil || skip {
			return skip, err
		}

		for _, shouldSkip := range artifactFilters {
			if shouldSkip(src) {
				return true, nil
			}
		}
		return false, nil
	}
}

// prepareBundle prepares a temporary directory for packing.
// Returns a path to the prepared directory or error if it failed.
func prepareBundle(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	cliOpts *config.CliOpts, buildRocks bool) (string, error) {
	var err error
	var signer integrity.Signer = nil

	// If integrity checks are enabled, create an IntegritySigner.
	if packCtx.IntegrityPrivateKey != "" {
		signer, err = integrity.NewSigner(packCtx.IntegrityPrivateKey)

		if err != nil {
			return "", err
		}
	}

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

	newOpts := createNewOpts(cliOpts, packCtx.CartridgeCompat)

	err = createPackageStructure(basePath, packCtx.CartridgeCompat, newOpts)
	if err != nil {
		return "", err
	}

	// Copy modules step.
	if !packCtx.CartridgeCompat && cliOpts.Modules != nil && cliOpts.Modules.Directory != "" &&
		!packCtx.WithoutModules {
		if err = copy.Copy(cliOpts.Modules.Directory,
			util.JoinPaths(basePath, newOpts.Modules.Directory)); err != nil {
			log.Warnf("Failed to copy modules from %q: %s", cliOpts.Modules.Directory, err)
		}
	}

	// Collect app list step.
	appList := []util.AppListEntry{}
	if packCtx.AppList == nil {
		appList, err = util.CollectAppList(cmdCtx.Cli.ConfigDir, cliOpts.Env.InstancesEnabled,
			true)
		if err != nil {
			return "", err
		}
	} else {
		for _, appName := range packCtx.AppList {
			if util.IsApp(filepath.Join(cliOpts.Env.InstancesEnabled, appName)) {
				appList = append(appList, util.AppListEntry{
					Name:     appName,
					Location: filepath.Join(cliOpts.Env.InstancesEnabled, appName),
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

	if packCtx.CartridgeCompat && len(appList) != 1 {
		err = fmt.Errorf("cannot pack multiple applications in compat mode")
		return "", err
	}

	{
		appsToPack := ""
		for _, appInfo := range appList {
			appsToPack += appInfo.Name + " "
		}
		if packCtx.CartridgeCompat {
			if packCtx.Name != "" {
				// Need to change application name.
				appList[0].Name = packCtx.Name
			} else {
				// Need to collect application name for
				// VERSION and VERSION.lua files.
				packCtx.Name = appList[0].Name
			}
		}
		log.Infof("Apps to pack: %s", appsToPack)
	}

	pkgBin := util.JoinPaths(basePath, newOpts.Env.BinDir)
	if packCtx.CartridgeCompat {
		pkgBin = util.JoinPaths(basePath, packCtx.Name)
	}
	// Copy binaries step.
	if cliOpts.Env.BinDir != "" &&
		((!packCtx.TarantoolIsSystem && !packCtx.WithoutBinaries) ||
			packCtx.WithBinaries) {
		err = copyBinaries(cmdCtx.Cli.TarantoolCli, pkgBin)
		if err != nil {
			return "", err
		}
	}

	// Copy all apps to a temp directory step.
	for _, appInfo := range appList {
		if packCtx.CartridgeCompat {
			err = copyAppSrc(appInfo.Location, appInfo.Name, basePath, skipArtifacts(cliOpts))
		} else {
			err = copyAppSrc(appInfo.Location, filepath.Base(appInfo.Location), basePath,
				skipArtifacts(cliOpts))
		}
		if err != nil {
			return "", err
		}

		if !packCtx.CartridgeCompat {
			err = createAppSymlink(appInfo.Location, appInfo.Name,
				util.JoinPaths(basePath, newOpts.Env.InstancesEnabled))
			if err != nil {
				return "", err
			}
		}
	}

	if packCtx.Archive.All {
		instances, err := running.CollectInstancesForApps(appList, cliOpts,
			cmdCtx.Cli.ConfigDir, cmdCtx.Integrity)
		if err != nil {
			return "", fmt.Errorf("failed to collect instances info: %w", err)
		}
		if err = copyArtifacts(*packCtx, basePath, cmdCtx.Cli.ConfigDir,
			cliOpts, newOpts, instances); err != nil {
			return "", err
		}
	}

	if buildRocks {
		err = buildAllRocks(cmdCtx, cliOpts, basePath)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}

	ttYamlPath := basePath
	if packCtx.CartridgeCompat {
		ttYamlPath = filepath.Join(ttYamlPath, appList[0].Name)
	}
	writeEnv(newOpts, ttYamlPath, packCtx.CartridgeCompat)
	if err != nil {
		return "", err
	}

	var appNames []string
	for _, app := range appList {
		appNames = append(appNames, app.Name)
	}

	if packCtx.IntegrityPrivateKey != "" {
		err = signer.Sign(basePath, appNames)
		if err != nil {
			return "", err
		}
	}

	return basePath, nil
}

// createPackageStructure initializes a standard package structure in passed directory.
func createPackageStructure(destPath string, cartridgeCompat bool,
	newCliOpts *config.CliOpts) error {
	basePaths := []string{destPath}

	if !cartridgeCompat {
		basePaths = append(
			basePaths,
			util.JoinPaths(destPath, newCliOpts.Env.BinDir),
			util.JoinPaths(destPath, newCliOpts.Modules.Directory),
			util.JoinPaths(destPath, newCliOpts.Env.IncludeDir),
			util.JoinPaths(destPath, newCliOpts.Env.InstancesEnabled),
		)
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
func copyAppSrc(appPath string, appName string, basePath string,
	skipFunc func(src string) (bool, error)) error {
	// In compat mode there must be only one application, so there will be no symlinks.
	// However, without the compat flag, encountering symlink must change appName.
	previousPath := appPath
	appPath, err := filepath.EvalSymlinks(previousPath)
	if err != nil {
		return err
	}

	// In compat mode will be false.
	if previousPath != appPath {
		appName = filepath.Base(appPath)
	}

	if _, err = os.Stat(appPath); err != nil {
		return err
	}

	// Copying application.
	log.Debugf("Copying application source %q -> %q", appPath, filepath.Join(basePath, appName))
	err = copy.Copy(appPath, filepath.Join(basePath, appName), copy.Options{
		Skip: func(src string) (bool, error) {
			return skipFunc(src)
		},
	})
	if err != nil {
		return err
	}
	return nil
}

// copyArtifacts copies all artifacts from the current bundle configuration
// to the passed package structure from the passed path.
func copyArtifacts(packCtx PackCtx, basePath string,
	configDir string, currentOpts, newOpts *config.CliOpts,
	instances []running.InstanceCtx) error {

	prevAppName := ""
	for _, inst := range instances {
		if inst.AppName != prevAppName {
			prevAppName = inst.AppName

			appDirName := filepath.Base(inst.AppDir)
			destAppDir := util.JoinPaths(basePath, newOpts.Env.InstancesEnabled, appDirName)
			newConfigDir := basePath
			if packCtx.CartridgeCompat {
				destAppDir = util.JoinPaths(basePath, packCtx.Name)
				newConfigDir = destAppDir
			}

			dstDir := func(dir string) string {
				if newOpts.Env.TarantoolctlLayout && inst.SingleApp {
					return util.JoinPaths(newConfigDir, dir)
				}
				return util.JoinPaths(destAppDir, dir)
			}
			copyInfo := []struct{ src, dest string }{}
			if newOpts.Env.TarantoolctlLayout && inst.SingleApp {
				copyInfo = append(copyInfo, struct{ src, dest string }{
					src:  inst.Log,
					dest: util.JoinPaths(dstDir(newOpts.App.LogDir), filepath.Base(inst.Log))})
			} else {
				copyInfo = append(copyInfo, struct{ src, dest string }{
					src:  filepath.Dir(inst.LogDir),
					dest: dstDir(newOpts.App.LogDir)})
			}
			copyInfo = append(copyInfo,
				struct{ src, dest string }{
					src: filepath.Dir(inst.WalDir), dest: dstDir(newOpts.App.WalDir)},
				struct{ src, dest string }{
					src: filepath.Dir(inst.MemtxDir), dest: dstDir(newOpts.App.MemtxDir)},
				struct{ src, dest string }{
					src: filepath.Dir(inst.VinylDir), dest: dstDir(newOpts.App.VinylDir)})
			if newOpts.App.WalDir == newOpts.App.VinylDir {
				copyInfo = copyInfo[:2]
			}
			for _, toCopy := range copyInfo {
				log.Debugf("Copying %q -> %q", toCopy.src, toCopy.dest)
				if err := copy.Copy(toCopy.src, toCopy.dest); err != nil {
					log.Warnf("Failed to copy artifacts: %s", err)
				}
			}
		}
	}
	return nil
}

// TODO replace by tt enable
// createAppSymlink creates a relative link for an application that must be packed.
func createAppSymlink(appPath string, appName string, instancesEnabledDir string) error {
	var err error
	appPath, err = filepath.EvalSymlinks(appPath)
	if err != nil {
		return err
	}

	err = os.Symlink(filepath.Join("..", filepath.Base(appPath)),
		filepath.Join(instancesEnabledDir, appName))
	if err != nil {
		return err
	}
	return nil
}

// createNewOpts generates new CLI opts using some data from current opts.
func createNewOpts(opts *config.CliOpts, cartridgeCompat bool) *config.CliOpts {
	log.Infof("Generating new %s for the new package", configure.ConfigName)
	cliOptsNew := configure.GetDefaultCliOpts()
	cliOptsNew.Env.InstancesEnabled = configure.InstancesEnabledDirName
	cliOptsNew.Env.Restartable = opts.Env.Restartable
	cliOptsNew.Env.TarantoolctlLayout = opts.Env.TarantoolctlLayout

	// In case the user separates one of the directories for storing memtx, vinyl or wal artifacts
	// the new environment will be also configured with separated standard directories for all
	// of them.
	if !((opts.App.VinylDir == opts.App.WalDir) && (opts.App.WalDir == opts.App.MemtxDir)) {
		cliOptsNew.App.VinylDir = configure.VarVinylPath
		cliOptsNew.App.MemtxDir = configure.VarMemtxPath
		cliOptsNew.App.WalDir = configure.VarWalPath
	}

	if cartridgeCompat {
		cliOptsNew.Env.InstancesEnabled = "."
		cliOptsNew.Env.BinDir = "."
	}

	return cliOptsNew
}

// writeEnv writes CLI options to a tt.yaml file.
func writeEnv(cliOpts *config.CliOpts, destPath string, cartridgeCompat bool) error {
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

	log.Debugf("Generating new environment config %q", file.Name())
	err = yaml.NewEncoder(file).Encode(cliOpts)
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

// getVersion returns a version of the package.
// The version depends on passed pack context.
func getVersion(packCtx *PackCtx, opts *config.CliOpts, defaultVersion string) string {
	packageVersion := defaultVersion
	if packCtx.Version == "" {
		// Get version from git only if packing an application from the current directory,
		// or packing with cartridge-compat enabled.
		var appPath = opts.Env.InstancesEnabled
		if opts.Env.InstancesEnabled != "." && packCtx.CartridgeCompat {
			appPath = filepath.Join(appPath, packCtx.Name)
		}
		if opts.Env.InstancesEnabled == "." || packCtx.CartridgeCompat {
			version, err := util.CheckVersionFromGit(appPath)
			if err == nil {
				packageVersion = version
				if packCtx.CartridgeCompat {
					if normalVersion, err := normalizeGitVersion(packageVersion); err == nil {
						packageVersion = normalVersion
					}
				}
			}
		}
		if packCtx.CartridgeCompat {
			packCtx.Version = packageVersion
		}
	} else {
		packageVersion = packCtx.Version
	}
	return packageVersion
}

// normalizeGitVersion edits raw version from `git describe` command.
func normalizeGitVersion(packageVersion string) (string, error) {
	var major = "0"
	var minor = "0"
	var patch = "0"
	var count = ""

	matched := false
	for _, r := range versionRgxps {
		matches := r.FindStringSubmatch(packageVersion)
		if matches != nil {
			matched = true
			for i, expName := range r.SubexpNames() {
				switch expName {
				case "Major":
					major = matches[i]
				case "Minor":
					minor = matches[i]
				case "Patch":
					patch = matches[i]
				case "Count":
					count = matches[i]
				}
			}
			break
		}
	}

	if !matched {
		return "", fmt.Errorf("git tag should be semantic (major.minor.patch)")
	}

	if count == "" {
		count = "0"
	}

	return fmt.Sprintf("%s.%s.%s.%s", major, minor, patch, count), nil
}

// copyBinaries copies tarantool and tt binaries from the current
// tt environment to the passed destination path.
func copyBinaries(tntCli cmdcontext.TarantoolCli, destPath string) error {
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

	tntBin, err := filepath.EvalSymlinks(tntCli.Executable)
	if err != nil {
		log.Warnf("Failed to access %s: %s", tntBin, err)
	}
	if tntBin == "" {
		tntBin = tntCli.Executable
	}

	err = copy.Copy(tntBin, filepath.Join(destPath, "tarantool"))
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
		if packCtx.CartridgeCompat {
			// Need to collect info about version
			// for generating VERSION and VERSION.lua files.
			getVersion(packCtx, opts, defaultLongVersion)
		}
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

// LuaGetRocksVersions gets map which contains {name: versions} from rocks manifest.
func LuaGetRocksVersions(appDirPath string) (RocksVersions, error) {
	rocksVersionsMap := RocksVersions{}

	manifestFilePath := filepath.Join(appDirPath, rocksManifestPath)
	if _, err := os.Stat(manifestFilePath); err == nil {
		L := lua.NewState()
		defer L.Close()

		if err := L.DoFile(manifestFilePath); err != nil {
			return nil, fmt.Errorf("failed to read manifest file %s: %s", manifestFilePath, err)
		}

		depsL := L.Env.RawGetString("dependencies")
		depsLTable, ok := depsL.(*lua.LTable)
		if !ok {
			return nil, fmt.Errorf("failed to read manifest file: dependencies is not a table")
		}

		depsLTable.ForEach(func(depNameL lua.LValue, depInfoL lua.LValue) {
			depName := depNameL.String()

			depInfoLTable, ok := depInfoL.(*lua.LTable)
			if !ok {
				log.Warnf("Failed to get %s dependency info", depName)
			} else {
				depInfoLTable.ForEach(func(depVersionL lua.LValue, _ lua.LValue) {
					rocksVersionsMap[depName] = append(rocksVersionsMap[depName],
						depVersionL.String())
				})
			}
		})

		for _, versions := range rocksVersionsMap {
			sort.Strings(versions)
		}

	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read manifest file %s: %s", manifestFilePath, err)
	}

	return rocksVersionsMap, nil
}
