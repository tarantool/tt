package pack

import (
	"fmt"
	"io/fs"
	"os"
	"path"
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
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/integrity"
	lua "github.com/yuin/gopher-lua"
	"gopkg.in/yaml.v2"
)

const (
	dirPermissions  = 0o750
	filePermissions = 0o666

	defaultVersion     = "0.1.0"
	defaultLongVersion = "0.1.0.0"

	versionFileName    = "VERSION"
	versionLuaFileName = "VERSION.lua"

	rocksManifestPath = ".rocks/share/tarantool/rocks/manifest"

	ignoreFile = ".packignore"
)

// spell-checker:ignore Rgxps

var versionRgxps = []*regexp.Regexp{
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

type skipFilter func(srcInfo os.FileInfo, src string) bool

type RocksVersions map[string][]string

// packFileInfo contains information to set for files/dirs in rpm/deb packages.
type packFileInfo struct {
	// owner is an owner of file/dir.
	owner string
	// group is a file/dir group.
	group string
}

// skipDefaults filters out sockets and git dirs.
func skipDefaults(srcInfo os.FileInfo, src string) bool {
	perm := srcInfo.Mode()
	if perm&os.ModeSocket != 0 {
		return true
	}

	if strings.HasPrefix(src, ".git") ||
		strings.Contains(src, "/.git") {

		return true
	}
	return false
}

// appArtifactsFilters returns a slice of skip functions to avoid copying application artifacts.
func appArtifactsFilters(cliOpts *config.CliOpts, srcAppPath string) []skipFilter {
	filters := make([]skipFilter, 0)
	if cliOpts.App == nil {
		return filters
	}

	for _, envDir := range [...]string{
		cliOpts.App.LogDir, cliOpts.App.RunDir, cliOpts.App.WalDir,
		cliOpts.App.MemtxDir, cliOpts.App.VinylDir,
	} {
		if envDir == "" {
			continue
		}
		appDir := filepath.Clean(envDir)
		if !filepath.IsAbs(envDir) {
			appDir = util.JoinPaths(srcAppPath, envDir)
		}
		if fileStat, err := os.Stat(appDir); err == nil {
			filters = append(filters, func(srcInfo os.FileInfo, src string) bool {
				return os.SameFile(srcInfo, fileStat)
			})
		}
	}
	return filters
}

// ttEnvironmentFilters prepares a slice of filters for tt environment directories/files.
func ttEnvironmentFilters(packCtx *PackCtx, cliOpts *config.CliOpts) []skipFilter {
	filters := make([]skipFilter, 0)
	if cliOpts == nil {
		return filters
	}

	envPaths := make([]string, 0, 6)
	if cliOpts.Env != nil {
		envPaths = append(envPaths, cliOpts.Env.IncludeDir,
			cliOpts.Env.InstancesEnabled, cliOpts.Env.BinDir)
	}
	if cliOpts.Modules != nil {
		envPaths = append(envPaths, cliOpts.Modules.Directories...)
	}
	if cliOpts.Repo != nil {
		envPaths = append(envPaths, cliOpts.Repo.Install)
	}
	for _, templatePath := range cliOpts.Templates {
		envPaths = append(envPaths, templatePath.Path)
	}
	envPaths = append(envPaths, packCtx.configFilePath)
	for _, envPath := range envPaths {
		if envPath == "" {
			continue
		}
		if fileStat, err := os.Stat(envPath); err == nil {
			filters = append(filters, func(srcInfo os.FileInfo, src string) bool {
				return os.SameFile(srcInfo, fileStat)
			})
		}
	}

	return filters
}

// previousPackageFilters returns filters for the previously built packages.
func previousPackageFilters(packCtx *PackCtx) []skipFilter {
	pkgName := packCtx.Name
	return []skipFilter{
		func(srcInfo os.FileInfo, src string) bool {
			name := srcInfo.Name()
			if strings.HasPrefix(name, pkgName) {
				for _, packageSuffix := range [...]string{".rpm", ".deb", ".gz", ".tgz"} {
					if filepath.Ext(name) == packageSuffix {
						return true
					}
				}
			}
			return false
		},
	}
}

// appSrcCopySkip returns a filter func to filter out artifacts paths.
func appSrcCopySkip(packCtx *PackCtx, cliOpts *config.CliOpts,
	srcAppPath string,
) (func(srcInfo os.FileInfo, src, dest string) (bool, error), error) {
	appCopyFilters := appArtifactsFilters(cliOpts, srcAppPath)
	appCopyFilters = append(appCopyFilters, ttEnvironmentFilters(packCtx, cliOpts)...)
	appCopyFilters = append(appCopyFilters, previousPackageFilters(packCtx)...)
	appCopyFilters = append(appCopyFilters, func(srcInfo os.FileInfo, src string) bool {
		return skipDefaults(srcInfo, src)
	})

	return func(srcInfo os.FileInfo, src, dest string) (bool, error) {
		for _, shouldSkip := range appCopyFilters {
			if shouldSkip(srcInfo, src) {
				return true, nil
			}
		}
		return packCtx.skipFunc(srcInfo, src, dest)
	}, nil
}

// getAppNamesToPack generates application names list to pack.
func getAppNamesToPack(packCtx *PackCtx, cliOpts *config.CliOpts) []string {
	if cliOpts.Env.InstancesEnabled == "." {
		return nil
	}
	appList := make([]string, len(packCtx.AppsInfo))
	i := 0
	for appName := range packCtx.AppsInfo {
		appList[i] = appName
		i = i + 1
	}
	return appList
}

// updateEnvPath sets base path for the tt environment in temporary package directory.
// By default it is a base directory passed as an argument. Or an application name sub-dir
// in case of cartridge compat or single application environment.
func updateEnvPath(basePath string, packCtx *PackCtx, cliOpts *config.CliOpts) (string, error) {
	if cliOpts.Env.InstancesEnabled == "." || packCtx.CartridgeCompat {
		basePath = util.JoinPaths(basePath, packCtx.Name)
		if err := os.MkdirAll(basePath, dirPermissions); err != nil {
			return basePath, fmt.Errorf("cannot create bundle directory %q: %s", basePath, err)
		}
	}
	return basePath, nil
}

// copyEnvModules copies tt modules.
func copyEnvModules(bundleEnvPath string, packCtx *PackCtx, cliOpts, newOpts *config.CliOpts) {
	if packCtx.WithoutModules || packCtx.CartridgeCompat || cliOpts.Modules == nil ||
		len(cliOpts.Modules.Directories) == 0 {

		return
	}

	rootEnvPath := filepath.Dir(packCtx.configFilePath)
	for _, directory := range cliOpts.Modules.Directories {
		if !strings.HasPrefix(directory, rootEnvPath) {
			log.Debugf("Skip copying external modules from %q: not a subdir of %q",
				directory, rootEnvPath)
			continue
		}
		if !util.IsDir(directory) {
			log.Debugf("Skip copying modules from %q: does not exist or not a directory",
				directory)
		} else {
			dir, err := os.Open(directory)
			if err != nil {
				log.Warnf("cannot open %q for reading: %s", directory, err)
			}
			if files, _ := dir.Readdir(1); len(files) == 0 {
				return // No modules.
			}
			dest := util.JoinPaths(bundleEnvPath, newOpts.Modules.Directories[0])
			err = copy.Copy(directory, dest, copy.Options{Skip: packCtx.skipFunc})
			if err != nil {
				log.Warnf("Failed to copy modules from %q: %s", directory, err)
			}
		}
	}
}

// copyBinaries copies binaries from current env to the result bundle.
func copyBinaries(bundleEnvPath string, packCtx *PackCtx, cmdCtx *cmdcontext.CmdCtx,
	newOpts *config.CliOpts,
) error {
	if packCtx.WithoutBinaries {
		return nil
	}

	pkgBin := bundleEnvPath
	if !packCtx.CartridgeCompat {
		// In cartridge compat mode copy binaries directly to the env dir.
		pkgBin = util.JoinPaths(bundleEnvPath, newOpts.Env.BinDir)
	}

	if err := os.MkdirAll(pkgBin, dirPermissions); err != nil {
		return fmt.Errorf("failed to create binaries directory in bundle: %s", err)
	}

	// Copy tarantool.
	if !packCtx.TarantoolIsSystem || packCtx.WithBinaries {
		if cmdCtx.Cli.TarantoolCli.Executable == "" {
			log.Warnf("Skip copying tarantool binary: not found")
		} else {
			if err := util.CopyFileDeep(cmdCtx.Cli.TarantoolCli.Executable,
				util.JoinPaths(pkgBin, "tarantool")); err != nil {
				return fmt.Errorf("failed copying tarantool: %s", err)
			}
		}
	}

	// Copy tt.
	ttExecutable, err := os.Executable()
	if err != nil {
		return err
	}
	if err := util.CopyFileDeep(ttExecutable, util.JoinPaths(pkgBin, "tt")); err != nil {
		return fmt.Errorf("failed copying tt: %s", err)
	}

	// Copy tcm.
	if cmdCtx.Cli.TcmCli.Executable == "" {
		log.Warnf("Skip copying tcm binary: not found")
	} else {
		if err := util.CopyFileDeep(cmdCtx.Cli.TcmCli.Executable,
			util.JoinPaths(pkgBin, "tcm")); err != nil {
			return fmt.Errorf("failed copying tcm: %w", err)
		}
	}
	return nil
}

// getDestAppDir returns application directory in the result bundle.
func getDestAppDir(bundleEnvPath, appName string,
	packCtx *PackCtx, cliOpts *config.CliOpts,
) string {
	if packCtx.CartridgeCompat || cliOpts.Env.InstancesEnabled == "." {
		return bundleEnvPath
	}
	return filepath.Join(bundleEnvPath, appName)
}

// copyApplications copies applications from current env to the result bundle.
func copyApplications(bundleEnvPath string, packCtx *PackCtx,
	cliOpts, newOpts *config.CliOpts,
) error {
	fmt.Printf("copyApplications: bundleEnvPath=%s\n", bundleEnvPath)
	var err error
	for appName, instances := range packCtx.AppsInfo {
		if len(instances) == 0 {
			return fmt.Errorf("application %q does not have any instances", appName)
		}
		inst := instances[0]
		appPath := inst.AppDir
		if inst.IsFileApp {
			appPath = inst.InstanceScript
			resolvedAppPath, err := filepath.EvalSymlinks(appPath)
			if err != nil {
				return err
			}
			if err = copy.Copy(resolvedAppPath,
				util.JoinPaths(bundleEnvPath, filepath.Base(resolvedAppPath))); err != nil {
				return fmt.Errorf("failed to copy application %q: %s", resolvedAppPath, err)
			}
		} else {
			bundleAppDir := getDestAppDir(bundleEnvPath, appName, packCtx, cliOpts)
			fmt.Printf("copyApplications: bundleAppDir=%s\n", bundleAppDir)
			if err = copyAppSrc(packCtx, cliOpts, appPath, bundleAppDir); err != nil {
				return err
			}
		}

		if !packCtx.CartridgeCompat && newOpts.Env.InstancesEnabled != "." {
			// Create applications symlink in instances enabled.
			if err = os.MkdirAll(util.JoinPaths(bundleEnvPath, newOpts.Env.InstancesEnabled),
				dirPermissions); err != nil {
				return fmt.Errorf("cannot create instances.enabled directory: %s", err)
			}
			packagingInstEnabledDir := util.JoinPaths(bundleEnvPath, newOpts.Env.InstancesEnabled)
			err = createAppSymlink(appPath, filepath.Base(appPath), packagingInstEnabledDir)
			if err != nil {
				return err
			}
			// Create working dir for script-only applications. This is required to do not attempt
			// to create it on target system which may lead to permissions denied error.
			if inst.IsFileApp {
				workingDir := util.JoinPaths(packagingInstEnabledDir, filepath.Base(inst.AppDir))
				if err = os.Mkdir(workingDir, dirPermissions); err != nil {
					return fmt.Errorf(
						"cannot create working directory %q for application: %s",
						workingDir, err)
				}
			}
		}
	}
	return nil
}

// prepareBundle prepares a temporary directory for packing.
// Returns a path to the prepared directory or error if it failed.
func prepareBundle(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	cliOpts *config.CliOpts, buildRocks bool,
) (string, error) {
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
	tmpDir, err := os.MkdirTemp("", "tt_pack")
	if err != nil {
		return "", err
	}

	defer func() {
		if err != nil {
			err := os.RemoveAll(tmpDir)
			if err != nil {
				log.Warnf("Failed to remove a directory %s: %s", tmpDir, err)
			}
		}
	}()
	bundleEnvPath := tmpDir

	packCtx.AppList = getAppNamesToPack(packCtx, cliOpts)
	log.Infof("Apps to pack: %s", strings.Join(packCtx.AppList, " "))

	if bundleEnvPath, err = updateEnvPath(bundleEnvPath, packCtx, cliOpts); err != nil {
		return "", err
	}
	newOpts := createNewOpts(cliOpts, *packCtx)

	copyEnvModules(bundleEnvPath, packCtx, cliOpts, newOpts)
	if err = copyBinaries(bundleEnvPath, packCtx, cmdCtx, newOpts); err != nil {
		return "", fmt.Errorf("error copying binaries: %s", err)
	}

	if err = copyApplications(bundleEnvPath, packCtx, cliOpts, newOpts); err != nil {
		return "", fmt.Errorf("error copying applications: %s", err)
	}

	// Copy tcm config, if any.
	if cmdCtx.Cli.TcmCli.ConfigPath != "" {
		dest := path.Join(bundleEnvPath, path.Base(cmdCtx.Cli.TcmCli.ConfigPath))
		err = copy.Copy(cmdCtx.Cli.TcmCli.ConfigPath, dest, copy.Options{Skip: packCtx.skipFunc})
		if err != nil {
			return "", fmt.Errorf("failed copying tcm config: %s", err)
		}
	}

	if packCtx.CartridgeCompat {
		// Generate VERSION file.
		if err := generateVersionFile(bundleEnvPath, cmdCtx, packCtx); err != nil {
			log.Warnf("Failed to generate VERSION file: %s", err)
		}

		// Generate VERSION.lua file.
		if err := generateVersionLuaFile(bundleEnvPath, packCtx); err != nil {
			log.Warnf("Failed to generate VERSION.lua file: %s", err)
		}
	}

	if packCtx.Archive.All {
		if err = copyArtifacts(*packCtx, bundleEnvPath, newOpts, packCtx.AppsInfo); err != nil {
			return "", fmt.Errorf("failed copying artifacts: %s", err)
		}
	}

	if buildRocks {
		err = buildAppRocks(cmdCtx, packCtx, cliOpts, bundleEnvPath)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}

	writeEnv(newOpts, bundleEnvPath, packCtx.CartridgeCompat)
	if err != nil {
		return "", err
	}

	if signer != nil {
		err = signer.Sign(bundleEnvPath, packCtx.AppList)
		if err != nil {
			return "", err
		}
	}

	return tmpDir, nil
}

// copyAppSrc copies a source file or directory to the directory, that will be packed.
func copyAppSrc(packCtx *PackCtx, cliOpts *config.CliOpts, srcAppPath, dstAppPath string) error {
	fmt.Printf("copyAppSrc: %q -> %q\n", srcAppPath, dstAppPath)
	resolvedAppPath, err := filepath.EvalSymlinks(srcAppPath)
	if err != nil {
		return err
	}
	if _, err = os.Stat(resolvedAppPath); err != nil {
		return err
	}

	skipFunc, err := appSrcCopySkip(packCtx, cliOpts, resolvedAppPath)
	if err != nil {
		return err
	}

	// Copying application.
	log.Debugf("Copying application source %q -> %q", resolvedAppPath, dstAppPath)
	return copy.Copy(resolvedAppPath, dstAppPath, copy.Options{Skip: skipFunc})
}

// copyArtifacts copies all artifacts from the current bundle configuration
// to the passed package structure from the passed path.
func copyArtifacts(packCtx PackCtx, basePath string, newOpts *config.CliOpts,
	appsInfo map[string][]running.InstanceCtx,
) error {
	for _, appName := range packCtx.AppList {
		for _, inst := range appsInfo[appName] {
			appDirName := filepath.Base(inst.AppDir)
			destAppDir := util.JoinPaths(basePath, newOpts.Env.InstancesEnabled, appDirName)
			if packCtx.CartridgeCompat || newOpts.Env.InstancesEnabled == "." {
				destAppDir = basePath
			}

			dstDir := func(dir string) string {
				if newOpts.Env.TarantoolctlLayout && inst.SingleApp {
					return util.JoinPaths(basePath, dir)
				}
				return util.JoinPaths(destAppDir, dir)
			}
			copyInfo := []struct{ src, dest string }{}
			if newOpts.Env.TarantoolctlLayout && inst.SingleApp {
				// Copy only one log file, not a directory. In case of tarantoolctl layout
				// application's log files are placed in the same directory with different
				// names. So to avoid copying log files of all applications, do not copy
				// the whole log dir.
				copyInfo = append(copyInfo, struct{ src, dest string }{
					src:  inst.Log,
					dest: util.JoinPaths(dstDir(newOpts.App.LogDir), filepath.Base(inst.Log)),
				})
			} else {
				copyInfo = append(copyInfo, struct{ src, dest string }{
					src:  filepath.Dir(inst.LogDir),
					dest: dstDir(newOpts.App.LogDir),
				})
			}
			copyInfo = append(copyInfo,
				struct{ src, dest string }{
					src: filepath.Dir(inst.WalDir), dest: dstDir(newOpts.App.WalDir),
				},
				struct{ src, dest string }{
					src: filepath.Dir(inst.MemtxDir), dest: dstDir(newOpts.App.MemtxDir),
				},
				struct{ src, dest string }{
					src: filepath.Dir(inst.VinylDir), dest: dstDir(newOpts.App.VinylDir),
				})
			if newOpts.App.WalDir == newOpts.App.VinylDir {
				copyInfo = copyInfo[:2]
			}
			for _, toCopy := range copyInfo {
				log.Debugf("Copying %q -> %q", toCopy.src, toCopy.dest)
				err := copy.Copy(toCopy.src, toCopy.dest, copy.Options{Skip: packCtx.skipFunc})
				if err != nil {
					log.Warnf("Failed to copy artifacts: %s", err)
				}
			}
		}
	}
	return nil
}

// TODO replace by tt enable
// createAppSymlink creates a relative link for an application that must be packed.
func createAppSymlink(appPath, appName, instancesEnabledDir string) error {
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
func createNewOpts(opts *config.CliOpts, packCtx PackCtx) *config.CliOpts {
	log.Infof("Generating new %s for the new package", configure.ConfigName)

	var cliOptsNew *config.CliOpts
	if packCtx.Type == Rpm || packCtx.Type == Deb {
		cliOptsNew = configure.GetSystemCliOpts()
		for _, varPath := range []*string{
			&cliOptsNew.App.WalDir, &cliOptsNew.App.MemtxDir,
			&cliOptsNew.App.VinylDir, &cliOptsNew.App.RunDir, &cliOptsNew.App.LogDir,
		} {
			*varPath = filepath.Join(*varPath, packCtx.Name)
		}
	} else {
		cliOptsNew = configure.GetDefaultCliOpts()
	}

	if opts.Env.InstancesEnabled != "." {
		cliOptsNew.Env.InstancesEnabled = configure.InstancesEnabledDirName
	}
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

	if packCtx.CartridgeCompat {
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

// rockspecExists tries to find a rockspec file in the passed directory.
func rockspecExists(root string) bool {
	rockSpecs, _ := filepath.Glob(filepath.Join(root, "*.rockspec"))
	return len(rockSpecs) > 0
}

// buildAppInBundle builds the application if a rockspec file exists.
func buildAppInBundle(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, appDir string) error {
	if !rockspecExists(appDir) {
		return nil
	}
	buildCtx := build.BuildCtx{BuildDir: appDir}
	if err := build.Run(cmdCtx, cliOpts, &buildCtx); err != nil {
		return fmt.Errorf("failed to build app %s: %s", filepath.Base(appDir), err)
	}
	return nil
}

// cleanupAfterBuild removes build files that are not needed for distribution.
func cleanupAfterBuild(appDir string) {
	// Remove rockspec files.
	rockspecs, _ := filepath.Glob(filepath.Join(appDir, "*.rockspec"))
	for _, rockspec := range rockspecs {
		log.Debugf("Removing %q", rockspec)
		if err := os.Remove(rockspec); err != nil {
			log.Warnf("cannot remove %q: %s", rockspec, err)
		}
	}

	// Remove pre/post build scripts.
	for _, buildScript := range append(build.PreBuildScripts, build.PostBuildScripts...) {
		scriptPath := filepath.Join(appDir, buildScript)
		if util.IsRegularFile(filepath.Join(appDir, buildScript)) {
			log.Debugf("Removing %q", scriptPath)
			if err := os.Remove(scriptPath); err != nil {
				log.Warnf("cannot remove %q: %s", scriptPath, err)
			}
		}
	}
}

// buildAppRocks finds a rockspec file of the application and builds it.
func buildAppRocks(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	cliOpts *config.CliOpts, bundlePath string,
) error {
	if cliOpts.Env.InstancesEnabled == "." || packCtx.CartridgeCompat {
		if err := buildAppInBundle(cmdCtx, cliOpts, bundlePath); err != nil {
			return err
		}
		cleanupAfterBuild(bundlePath)
	}

	for appName := range packCtx.AppsInfo {
		appDir := filepath.Join(bundlePath, appName)
		if util.IsDir(appDir) {
			if err := buildAppInBundle(cmdCtx, cliOpts, appDir); err != nil {
				return err
			}
			cleanupAfterBuild(appDir)
		}
	}

	return nil
}

// getVersion returns a version of the package.
// The version depends on passed pack context.
func getVersion(packCtx *PackCtx, opts *config.CliOpts, defaultVersion string) string {
	packageVersion := defaultVersion
	if packCtx.Version == "" {
		// Get version from git only if packing an application from the current directory,
		// or packing with cartridge-compat enabled.
		appPath := opts.Env.InstancesEnabled
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
	major := "0"
	minor := "0"
	patch := "0"
	count := ""

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

// getPackageFileName returns the result name of the package file.
func getPackageFileName(packCtx *PackCtx, opts *config.CliOpts, suffix string,
	addVersion bool,
) (string, error) {
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

		depsLTable.ForEach(func(depNameL, depInfoL lua.LValue) {
			depName := depNameL.String()

			depInfoLTable, ok := depInfoL.(*lua.LTable)
			if !ok {
				log.Warnf("Failed to get %s dependency info", depName)
			} else {
				depInfoLTable.ForEach(func(depVersionL, _ lua.LValue) {
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

// updatePermissions updates permissions for packaging files/dirs to make it work under
// non-root user on target system.
func updatePermissions(baseDir string) func(path string, entry fs.DirEntry, err error) error {
	return func(path string, entry fs.DirEntry, err error) error {
		if entry.IsDir() {
			if err := os.Chmod(filepath.Join(baseDir, path), 0o755); err != nil {
				log.Warnf("failed to to change permissions of %q: %s", path, err)
			}
		}
		return nil
	}
}

// createArtifactsDirs creates /var/<dir>/tarantool/<env> artifact directories in rpm/deb package
// and sets owner and group info for these directories.
func createArtifactsDirs(pkgDataDir string, packCtx *PackCtx) error {
	for _, dirToCreate := range []string{
		configure.VarDataPath, configure.VarLogPath,
		configure.VarRunPath,
	} {
		artifactEnvDir := filepath.Join(pkgDataDir, dirToCreate, "tarantool")
		if err := os.MkdirAll(artifactEnvDir, dirPermissions); err != nil {
			return fmt.Errorf("cannot create %q: %s", artifactEnvDir, err)
		}
	}
	for _, dir := range [...]string{"lib", "log", "run"} {
		packCtx.RpmDeb.pkgFilesInfo[fmt.Sprintf("var/%s/tarantool", dir)] = packFileInfo{
			"tarantool",
			"tarantool",
		}
	}
	return nil
}
