package install

import (
	_ "embed"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/docker"
	"github.com/tarantool/tt/v3/cli/templates"
	"github.com/tarantool/tt/v3/internal/binary"
)

const dockerfileMode = 0o664

//go:embed Dockerfile.tnt.build
var tarantoolBuildDockerfile []byte

func installTarantoolInDocker(tntVersion, binDir, incDir string, installCtx InstallCtx,
) error {
	tmpDir, err := os.MkdirTemp("", "docker_build_ctx")
	if err != nil {
		return fmt.Errorf("failed to create a temporary directory: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	goTextEngine := templates.NewDefaultEngine()

	dockerfileText, err := goTextEngine.RenderText(string(tarantoolBuildDockerfile),
		map[string]string{"uid": currentUser.Uid})
	if err != nil {
		return err
	}

	// Write docker file (rw-rw-r-- permissions).
	err = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfileText),
		dockerfileMode)
	if err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Copy tt executable.
	currentExecutable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get tt executable path: %w", err)
	}

	err = copy.Copy(currentExecutable, filepath.Join(tmpDir, "tt"))
	if err != nil {
		return nil
	}

	// Generate tt config for tt process in the container.
	ttCfg := configure.GetDefaultCliOpts()

	ttCfg.Env.BinDir = "/tt_bin"
	ttCfg.Env.IncludeDir = "/tt_include"
	ttCfg.Repo.Install = "/tt_distfiles"

	err = binary.WriteYaml(filepath.Join(tmpDir, configure.ConfigName), ttCfg)
	if err != nil {
		return err
	}

	// Generate install command line for tt in container.
	tntInstallCommandLine := []string{"./tt", "binary", "--scope", installCtx.Scope.String()}
	if installCtx.Verbose {
		tntInstallCommandLine = append(tntInstallCommandLine, "-V")
	}

	tntInstallCommandLine = append(tntInstallCommandLine, "install", "-f",
		binary.ProgramCe.Exec(), tntVersion)
	if installCtx.Reinstall {
		tntInstallCommandLine = append(tntInstallCommandLine, "--reinstall")
	}

	if installCtx.Local {
		tntInstallCommandLine = append(tntInstallCommandLine, "--local-repo")
	}

	if installCtx.Dynamic {
		tntInstallCommandLine = append(tntInstallCommandLine, "--dynamic")
	}

	// Exclude last element from incDir path, because it already has "include" subdir appended.
	// So we get the parent of incDir to get original include path.
	dockerRunOptions := docker.RunOptions{
		BuildCtxDir: tmpDir,
		ImageTag:    "ubuntu:tt_tarantool_build",
		Command:     tntInstallCommandLine,
		Binds: []string{
			fmt.Sprintf("%s:%s", binDir, ttCfg.Env.BinDir),
			fmt.Sprintf("%s:%s", filepath.Dir(incDir), ttCfg.Env.IncludeDir),
			fmt.Sprintf("%s:%s", installCtx.localRepoDir, ttCfg.Repo.Install),
		},
		Verbose: installCtx.Verbose,
	}

	return docker.RunContainer(dockerRunOptions, os.Stdout)
}
