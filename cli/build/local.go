package build

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/rocks"
	"github.com/tarantool/tt/cli/util"
)

const (
	// List of directories specifying a search path for cmake find_file and find_path commands.
	cmakeIncludePathEnvVar = "CMAKE_INCLUDE_PATH"
)

var (
	// PreBuildScripts is a list of pre-build script names.
	PreBuildScripts = []string{"tt.pre-build", "cartridge.pre-build"}
	// PostBuildScripts is a list of post-build script names.
	PostBuildScripts = []string{"tt.post-build", "cartridge.post-build"}
)

// runBuildHook runs first existing executable from hookNames list.
func runBuildHook(buildCtx *BuildCtx, hookNames []string) error {
	for _, hookName := range hookNames {
		buildHookPath := filepath.Join(buildCtx.BuildDir, hookName)

		if _, err := os.Stat(buildHookPath); err == nil {
			log.Infof("Running `%s`", buildHookPath)
			err = util.RunHook(buildHookPath, false)
			if err != nil {
				return fmt.Errorf("failed to run build hook: %s", err)
			}
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to run build hook: %s", err)
		}
	}

	return nil
}

// buildLocal builds an application locally.
func buildLocal(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, buildCtx *BuildCtx) error {
	cancelChdir, err := util.Chdir(buildCtx.BuildDir)
	if err != nil {
		return err
	}
	defer cancelChdir()

	// Run Pre-build.
	if err := runBuildHook(buildCtx, PreBuildScripts); err != nil {
		return fmt.Errorf("run pre-build hook failed: %s", err)
	}

	// Setting env var for luarocks to make cmake to find tarantool includes.
	includeDir := filepath.Join(cliOpts.Env.IncludeDir, "include")
	if util.IsDir(includeDir) {
		log.Debugf("Setting Tarantool include path: %q", cliOpts.Env.IncludeDir)
		os.Setenv(cmakeIncludePathEnvVar, cliOpts.Env.IncludeDir)
	}

	// Run rocks make.
	log.Infof("Running rocks make")

	savedStdoutFd := syscall.Stdout
	if !cmdCtx.Cli.Verbose {
		// Redirect stdout to /dev/null.
		if savedStdoutFd, err = syscall.Dup(syscall.Stdout); err != nil {
			return err
		}
		defer syscall.Close(savedStdoutFd)

		var devNull *os.File = nil
		if devNull, err = os.OpenFile(os.DevNull, os.O_WRONLY, 0o666); err != nil {
			return err
		}
		defer devNull.Close()

		if err = Dup2(int(devNull.Fd()), syscall.Stdout); err != nil {
			return err
		}
		defer Dup2(savedStdoutFd, syscall.Stdout)
	}

	rocksMakeCmd := []string{"make"}
	if buildCtx.SpecFile != "" {
		rocksMakeCmd = append(rocksMakeCmd, buildCtx.SpecFile)
	}
	if err := rocks.Exec(cmdCtx, cliOpts, rocksMakeCmd); err != nil {
		return err
	}
	if err := Dup2(savedStdoutFd, syscall.Stdout); err != nil {
		return err
	}

	// Run Post-build.
	if err := runBuildHook(buildCtx, PostBuildScripts); err != nil {
		return fmt.Errorf("run post-build hook failed: %s", err)
	}

	log.Info("Application was successfully built")

	return nil
}
